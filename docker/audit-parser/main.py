#!/usr/bin/env python
import os
import re
import sys
import json
import shlex
import collections

import click

from lxml import etree


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
        if not doc.attrib['event'] == "execve(2)":
            continue
        data = {}
        data.update(doc.attrib)
        data['path'], data['realpath'] = doc.xpath('//path/text()')
        data['argv'] = doc.xpath('//exec_args/arg/text()')
        data['attrs'] = dict(doc.xpath('//attribute')[0].attrib)
        data['subject'] = dict(doc.xpath('//subject')[0].attrib)
        data['return'] = dict(doc.xpath('//return')[0].attrib)
        yield data


@click.command()
@click.option('--exclude-patterns', '-x', type=click.Path(exists=True),
              help='Path to file containing exclude patterns.')
def main(exclude_patterns):
    #for line in sys.stdin:
    if exclude_patterns:
        with open(click.format_filename(exclude_patterns)) as f:
            exclude_argv = ExcludeArgv(f)
    #with open('/tmp/cowaudit') as f:
    with sys.stdin as f:
        next(f)
        next(f)
        for event in parse_events(f):
#            if 'rev-parse' in ' '.join(event['argv']):
#                import rpdb; rpdb.set_trace()
            if event['argv'] in exclude_argv:
                continue
            else:
                sys.stdout.write(json.dumps(event))
                sys.stdout.write('\n')



if __name__ == '__main__':
    main()
