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

import click

from lxml import etree

from elasticsearch import Elasticsearch


es = Elasticsearch()


def get_logger(socket_host, socket_port):
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
            'webhook-vertica-admin': {
                'handlers': ['console'],
                'level': logging.DEBUG,
                'propagate': True,
                },
            }
        }

    config.dictConfig(LOGGING)
    logger = logging.getLogger("webhook-vertica-admin")
    return logger
logger = logging.getLogger('audit-forwarder')


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


def parse_events(fp):
    for line in fp:
        try:
            doc = etree.fromstring(line)
        except etree.XMLSyntaxError:
            continue

        if doc.attrib.get('event', None) in (
              "pthread_sigmask(2)",
              "sysctl() - non-admin",
              "recvmsg(2)",
              "fcntl(2)"):
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
            #continue
            import pdb; pdb.set_trace()
        else:
            if len(paths) == 2:
                data['path'], data['realpath'] = paths
            elif len(paths) == 1:
                data['path'] = data['realpath'] = paths
            else:
                logger.warning('No paths found for event: %r', line)
                #raise ValueError('More than two path elements found: %r' % line)

        try:
            # Get the command-line arguments.
            data['argv'] = doc.xpath('//exec_args/arg/text()')
        except Exception as exc:
            msg = 'xpath "//exec_args/arg/text()" failed for event: %r'
            logger.exception(msg, line)
            #continue
            import pdb; pdb.set_trace()

        try:
            # Get the command-line arguments.
            envraw = doc.xpath('//exec_env/env/text()')
        except Exception as exc:
            msg = 'xpath "//exec_env/env/text()" failed for event: %r'
            logger.exception(msg, line)
            #continue
            import pdb; pdb.set_trace()

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
        data['@timestamp'] = dt

        #data['argv'] = ' '.join(data['argv'])

        yield data


@click.command()
@click.option('--exclude-patterns', '-x', type=click.Path(exists=True),
              help='Path to file containing exclude patterns.')
def main(exclude_patterns):
    if exclude_patterns:
        with open(click.format_filename(exclude_patterns)) as f:
            exclude_argv = ExcludeArgv(f)
    cmd = ['./auditfilter.sh']
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    f = iter(proc.stdout)
    next(f)
    next(f)
    for event in parse_events(f):
        if event['argv'] in exclude_argv:
            continue
        else:
            res = es.index(index="audit", doc_type='event', body=event)
#            pprint.pprint(event)
            print(res)



if __name__ == '__main__':
    main()
