#!/usr/bin/env python3.6
"""Example for aiohttp.web basic server
"""
import json
import base64
import asyncio
import textwrap

from aiohttp.web import Application, Response, StreamResponse, run_app
import asyncio_redis


async def collect(request):
    params = dict(await request.json())
    params['command'] = str(base64.decodestring(bytes(params['command'], 'utf')), 'utf8')
    print(params)

    connection = await asyncio_redis.Connection.create('redis')
    await connection.publish('totalrecall', json.dumps(params))

    resp = StreamResponse()
    resp.content_type = 'text/plain'
    await resp.prepare(request)
    resp.write(b'Ok.\n')
    return resp


async def init(loop):
    app = Application()
    app.router.add_put('/', collect)
    return app


loop = asyncio.get_event_loop()
app = loop.run_until_complete(init(loop))
run_app(app)
