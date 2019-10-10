import io
import os
import json
import time
import atexit
import locale
import curses
import textwrap
import itertools
import traceback
from datetime import datetime, timedelta
from curses import KEY_RIGHT, KEY_LEFT, KEY_DOWN, KEY_UP
from random import randint

import asyncio
import asyncio_redis
from elasticsearch_async import AsyncElasticsearch
from terminaltables import AsciiTable

WIDTH = 120
HEIGHT = 30


class App:

    WIDTH = 120
    HEIGHT = 50
    MAX_X = WIDTH - 2
    MAX_Y = HEIGHT - 2
    TIMEOUT = 0

    def setup(self):
        locale.setlocale(locale.LC_ALL, '')
        code = locale.getpreferredencoding()
        self._stdscr = curses.initscr()
        curses.start_color()
        self.window = window = curses.newwin(self.HEIGHT, self.WIDTH, 0, 0)
        window.timeout(self.TIMEOUT)
        window.keypad(True)
        curses.noecho()
        curses.cbreak()

        def _shutdown():
            curses.nocbreak()
            self._stdscr.keypad(False)
            curses.echo()
            curses.endwin()
        atexit.register(_shutdown)

    def teardown(self):
        curses.nocbreak()
        self._stdscr.keypad(False)
        curses.echo()
        curses.endwin()

    def __enter__(self):
        self.setup()
        return self

    def __exit__(self, exc_type, exc, tb):
        self.teardown()
        if tb is not None:
            traceback.print_tb(tb)


async def get_command_history(es, data, relevance=True):
    MAXROWS = 30

    # --------------------------------------------------------------
    # Get the table data
    # --------------------------------------------------------------
    query = {'query': {'match_all': {}}}
    query = {
        "from": 0,
        "size": 500,
        "query": {
            "bool": {
            "should": [],
            "must": [],
            }
        }
    }
    query['query']['bool']['must'].append({'match': {'return_code': 0}})

    predicates = dict(PWD='must')
    for key, val in data['env'].items():
        pred = predicates.get(key, 'should')
        query['query']['bool'][pred].append({'match': {'env.' + key: val}})

    if not relevance:
        query['sort'] = {"start_timestamp": {"order": "desc"},
                        "_score": {"order": "desc"}}

    results = await es.search(index='totalrecall', body=query)

    tabledata = [['Score', 'Command']]

    dedupe = set()
    for i, result in enumerate(results['hits']['hits']):
        cmd = result['_source']['command']
        score = result['_score']
        if cmd not in dedupe:
            tabledata.append([score, textwrap.fill(cmd, 70)])
        dedupe.add(cmd)
        if MAXROWS <= len(dedupe):
            break

    table = AsciiTable(tabledata)
    return table


async def main():
    connection = await asyncio_redis.Connection.create('localhost', 16379)
    es = AsyncElasticsearch(hosts=['localhost'])

    relevance = True
    cmd = None

    try:
        # Subscribe to a channel.
        subscriber = await connection.start_subscribe()
        await subscriber.subscribe(['totalrecall'])
        with App() as app:
           while True:
                reply = await subscriber.next_published()
                if reply.value == 'q':
                    break
                data = json.loads(reply.value)

                table1 = await get_command_history(es, data, relevance=True)
                table2 = await get_command_history(es, data, relevance=False)
                # --------------------------------------------------------------
                # Update the view.
                # --------------------------------------------------------------
                app.window.clear()
                app.window.addstr(0, 0, 'Commands for cwd=%s' % data['env']['PWD'], curses.A_BOLD)
                # Add the relevance table
                app.window.addstr(3, 0, table1.table)
                # Add the chronological table
#                app.window.addstr(len(table1.table.splitlines()) + 4, 0, table2.table)
                app.window.noutrefresh()
                curses.doupdate()
    finally:
        connection.close()


if __name__ == '__main__':
    loop = asyncio.get_event_loop()
    loop.run_until_complete(main())

