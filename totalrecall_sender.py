#!/usr/bin/env python3
import os
import sys
import json
import socket
import base64
import datetime
import urllib.parse

import click


@click.command()
@click.option('--command')
@click.option('--return-code', type=int)
@click.option('--start-timestamp')
@click.option('--end-timestamp', default=None)
@click.option('--url', default='tcp://127.0.0.1:5170/')
def main(command, return_code, start_timestamp, end_timestamp, url):
    if end_timestamp is None:
        end_timestamp = datetime.datetime.utcnow()

    host, port = '127.0.0.1', 5170
    url = urllib.parse.urlparse(url)
    if ':' in url.netloc:
        host, port = url.netloc.split(':')
    else:
        host = url.netloc
    port = int(port)

    # Nuke internally used env vars.
    env = dict(os.environ)
    for key in list(env.keys()):
        if key.startswith('___PREEXEC_'):
            del env[key]

    command = str(base64.decodebytes(bytes(command, 'utf8')), 'utf8').strip()
    payload = dict(
        command=command,
        env=env,
        return_code=return_code,
        start_timestamp=start_timestamp,
        end_timestamp=end_timestamp,
    )

    BUFFER_SIZE = 1024
    MESSAGE = bytes(json.dumps(payload), 'utf8')

    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.connect((host, port))
    s.send(MESSAGE)
    data = s.recv(BUFFER_SIZE)
    s.close()


if __name__ == "__main__":
    main()
