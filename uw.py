from __future__ import print_function

import os
import asyncio
from datetime import datetime
import sys
import weakref

import urwid
from urwid.raw_display import Screen


loop = asyncio.get_event_loop()

import asyncio.subprocess
import pprint
# -----------------------------------------------------------------------------
# General-purpose setup code

def build_widgets():
    def update_text(widget_ref):
        widget = widget_ref()
        if not widget:
            # widget is dead; the main loop must've been destroyed
            return
        dirs = os.listdir(dr)
        text = pprint.pformat(dirs)

        widget.set_text(text)

        # Schedule us to update the clock again in one second
        loop.call_later(1, update_text, widget_ref)

    clock = urwid.Text('')
    update_text(weakref.ref(clock))

    return urwid.Filler(urwid.Pile([clock] + inputs), 'top')


def unhandled(key):
    if key == 'ctrl c':
        raise urwid.ExitMainLoop


# -----------------------------------------------------------------------------
# Demo 1

def demo1():
    """Plain old urwid app.  Just happens to be run atop asyncio as the event
    loop.
    Note that the clock is updated using the asyncio loop directly, not via any
    of urwid's facilities.
    """
    main_widget = build_widgets()

    urwid_loop = urwid.MainLoop(
        main_widget,
        event_loop=urwid.AsyncioEventLoop(loop=loop),
        unhandled_input=unhandled,
    )
    urwid_loop.run()


if __name__ == '__main__':
    demo1()

