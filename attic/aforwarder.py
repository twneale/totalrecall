#!/usr/bin/env python
import click
import asyncio.subprocess
from aiokafka import AIOKafkaProducer


@click.command()
def main():
    async def produce_events():
        producer = AIOKafkaProducer(
            loop=loop, bootstrap_servers='localhost:9092')
        # Get cluster layout and initial topic/partition leadership information
        await producer.start()

        # Create the subprocess, redirect the standard output into a pipe
        proc = await asyncio.create_subprocess_exec(
            './auditfilter.sh', stdout=asyncio.subprocess.PIPE)

        # Skip two lines of output
        data = await proc.stdout.readline()
        data = await proc.stdout.readline()

        while True:
            data = await proc.stdout.readline()
            fut = await producer.send_and_wait("audit-raw", data)

    loop = asyncio.get_event_loop()
    date = loop.run_until_complete(produce_events())
    loop.close()


if __name__ == '__main__':
    main()
