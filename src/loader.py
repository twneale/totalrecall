#!/usr/bin/env python3.6
import json
import asyncio
import asyncio_redis

from elasticsearch_async import AsyncElasticsearch


async def main():
    client = AsyncElasticsearch(hosts=['elasticsearch'])
    connection = await asyncio_redis.Connection.create('redis')
    try:
        # Subscribe to a channel.
        subscriber = await connection.start_subscribe()
        await subscriber.subscribe(['totalrecall'])

        # Print published values in a while/true loop.
        while True:
            reply = await subscriber.next_published()
            print(reply)
            event = json.loads(reply.value)
            try:
                await client.index(index="totalrecall", doc_type='event', body=event)
            except Exception as exc:
                print(exc)

    finally:
        connection.close()

if __name__ == "__main__":
    loop = asyncio.get_event_loop()
    loop.run_until_complete(main())
