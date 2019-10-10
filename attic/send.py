#!/usr/bin/env python3
import os
import sys
import json
import datetime

import click
import requests


@click.command()
@click.option('--command')
@click.option('--return-code', type=int)
@click.option('--start-timestamp')
@click.option('--end-timestamp', default=None)
@click.option('--url', default='http://localhost:8080/')
def main(command, return_code, start_timestamp, end_timestamp, url):
    if end_timestamp is None:
        end_timestamp = datetime.datetime.utcnow()

    # Nuke internally used env vars.
    env = dict(os.environ)
    for key in list(env.keys()):
        if key.startswith('___PREEXEC_'):
            del env[key]

    payload = dict(
        command=command.strip(),
        env=env,
        return_code=return_code,
        start_timestamp=start_timestamp,
        end_timestamp=end_timestamp,
    )
#    import pprint
#    pprint.pprint(payload)
    resp = requests.put(url, json=payload)


if __name__ == "__main__":
    main()
