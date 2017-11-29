#!/usr/bin/env python
import os
import re
import sys
import pwd
import json
import time
import shlex
import pprint
import datetime
import subprocess
import collections

import logging
import logging.config

import click
from lxml import etree
from aiokafka import AIOKafkaConsumer, AIOKafkaProducer
import asyncio


def get_logger():
    LOGGING = {
        'version': 1,
        'disable_existing_loggers': False,
        'formatters': {
            'verbose': {
                'format': 'time="%(asctime)s" logger="%(name)s" level="%(levelname)s" message="%(message)s"'
                },
            },
        'handlers': {
            'console':{
                'level':'DEBUG',
                'class':'logging.StreamHandler',
                'formatter': 'verbose'
            }
        },
        'loggers': {
            'audit-parser': {
                'handlers': ['console'],
                'level': logging.DEBUG,
                'propagate': True,
                },
            }
        }

    logging.config.dictConfig(LOGGING)
    logger = logging.getLogger("audit-parser")
    return logger

logger = get_logger()


class ExcludeArgv:

    def __init__(self, fp):
        self._fp = fp
        self._prep_data()

    def _prep_data(self):
        self.data = set()
        for line in self._fp:
            argv = shlex.split(line)
            argv_set = self.argv2set(argv)
            self.data.add(argv_set)

    def argv2set(self, argv):
        argv_set = frozenset(enumerate(argv))
        return argv_set

    def __contains__(self, argv):
        return self.argv2set(argv) in self.data


@click.command()
@click.option('--exclude-patterns', '-x', type=click.Path(exists=True),
              help='Path to file containing exclude patterns.')
def main(exclude_patterns):
    if exclude_patterns:
        with open(click.format_filename(exclude_patterns)) as f:
            exclude_argv = ExcludeArgv(f)

    loop = asyncio.get_event_loop()

    async def consume():

        consumer = AIOKafkaConsumer(
            'audit-raw',
            loop=loop, bootstrap_servers='localhost:9092')
        await consumer.start()

        producer = AIOKafkaProducer(
            loop=loop, bootstrap_servers='localhost:9092')
        await producer.start()

        try:
            # Consume messages
            async for msg in consumer:
                line = str(msg.value, 'ascii')
                try:
                    doc = etree.fromstring(line)
                except etree.XMLSyntaxError:
                    continue

                #if not doc.attrib.get('event', None) == "execve(2)":
                #    continue

                data = {}
                data.update(doc.attrib)
                data.pop('msec')
                data.pop('version')

                try:
                    # Get the path to the executable.
                    paths = doc.xpath('//path/text()')
                except Exception as exc:
                    msg = 'xpath "//path/text()" failed for event: %r'
                    logger.exception(msg, line)
                    continue
                else:
                    if len(paths) == 2:
                        data['path'], data['realpath'] = paths
                    elif len(paths) == 1:
                        data['path'] = data['realpath'] = paths
                    else:
                        logger.warning('No paths found for event: %r', line)

                try:
                    # Get the command-line arguments.
                    data['argv'] = doc.xpath('//exec_args/arg/text()')
                except Exception as exc:
                    msg = 'xpath "//exec_args/arg/text()" failed for event: %r'
                    logger.exception(msg, line)

                try:
                    # Get the command-line arguments.
                    envraw = doc.xpath('//exec_env/env/text()')
                except Exception as exc:
                    msg = 'xpath "//exec_env/env/text()" failed for event: %r'
                    logger.exception(msg, line)

                env = data['env'] = {}
                for string in envraw:
                    key, _, val = string.partition('=')
                    env[key] = val

                try:
                    # Get the subject packet and remove useless info.
                    subject = dict(doc.xpath('//subject')[0].attrib)
                except Exception as exc:
                    msg = 'xpath "//subject" failed for event: %r'
                    logger.exception(msg, line)
                else:
                    data['subject'] = subject

                try:
                    data['return'] = dict(doc.xpath('//return')[0].attrib)
                except Exception as exc:
                    msg = 'xpath "//return" failed for event: %r'
                    logger.exception(msg, line)

                # Get the timestamp.
                dt = datetime.datetime.strptime(data.pop('time'), '%a %b  %d %H:%M:%S %Y')
                data['@timestamp'] = dt.isoformat()

                if data['argv'] in exclude_argv:
                    continue
                await producer.send_and_wait("audit-json", bytes(json.dumps(data), 'utf8'))
        finally:
            # Will leave consumer group; perform autocommit if enabled.
            await consumer.stop()
            await producer.stop()

    loop.run_until_complete(consume())


if __name__ == '__main__':
    main()
