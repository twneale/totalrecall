import os
import json
import time
import atexit
import locale
import curses
import traceback
import itertools
from datetime import datetime, timedelta
from curses import KEY_RIGHT, KEY_LEFT, KEY_DOWN, KEY_UP
from random import randint

import asyncio
import asyncio_redis


WIDTH = 120
HEIGHT = 30


class App:

    WIDTH = 120
    HEIGHT = 30
    MAX_X = WIDTH - 2
    MAX_Y = HEIGHT - 2
    TIMEOUT = 0

    def setup(self):
        locale.setlocale(locale.LC_ALL, '')
        code = locale.getpreferredencoding()
        self._stdscr = curses.initscr()
        self.window = window = curses.newwin(self.HEIGHT, self.WIDTH, 0, 0)
        window.timeout(self.TIMEOUT)
        window.keypad(True)
        curses.noecho()
        curses.cbreak()

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


async def main():
    connection = await asyncio_redis.Connection.create('localhost', 16379)

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

                app.window.clear()
                app.window.border(0)
                x = 1
                y = 3

                for thing in os.listdir(data['env']['PWD']):
                    if os.path.isdir(os.path.join(data['env']['PWD'], thing)):
                        thing = thing + '/'
                    try:
                        app.window.addstr(x, y, thing)
                    except:
                        continue
                    x += 1
                app.window.noutrefresh()
                curses.doupdate()
    finally:
        connection.close()


if __name__ == '__main__':

    loop = asyncio.get_event_loop()
    loop.run_until_complete(main())

