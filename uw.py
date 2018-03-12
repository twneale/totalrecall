from __future__ import print_function

import os
import asyncio
from datetime import datetime
import sys
import weakref

import urwid
from urwid.raw_display import Screen


import pprint

#!/usr/bin/env python3.6
import json
import asyncio
import asyncio_redis

from elasticsearch_async import AsyncElasticsearch


# -----------------------------------------------------------------------------
# General-purpose setup code

def build_widgets(loop):
    async def update_text(widget_ref):
        widget = widget_ref()
        if not widget:
            # widget is dead; the main loop must've been destroyed
            return
        connection = await asyncio_redis.Connection.create('localhost', 16379)
        #try:
        # Subscribe to a channel.
        subscriber = await connection.start_subscribe()
        await subscriber.subscribe(['totalrecall'])

        # Print published values in a while/true loop.
        reply = await subscriber.next_published()
        event = json.loads(reply.value)
        #print(event)
        pwd = event['_source']['env']['PWD']
        #try:
        dirs = os.listdir(pwd)
        text = pprint.pformat(dirs)
        widget.set_text(text)
        #except Exception as exc:
        #print(exc)
    #finally:
    #    connection.close()


        #loop.create_task(update_text(weakref.ref(widget)))

    widget = urwid.Text('')
    print('creating task')
    loop.create_task(update_text(weakref.ref(widget)))
    print('created task')

    return urwid.Filler(urwid.Pile([widget]), 'top')


def unhandled(key):
    if key == 'ctrl c':
        raise urwid.ExitMainLoop


# -----------------------------------------------------------------------------
# Demo 1

def main(loop):
    print('building widgets')
    main_widget = build_widgets(loop)
    print('built widgets')
    print('creating loop')
    urwid_loop = urwid.MainLoop(
        main_widget,
        event_loop=urwid.AsyncioEventLoop(loop=loop),
        unhandled_input=unhandled,
    )
    print('running loop')
    urwid_loop.run()


if __name__ == "__main__":
    loop = asyncio.get_event_loop()
    loop.set_debug(True)
    main(loop)
