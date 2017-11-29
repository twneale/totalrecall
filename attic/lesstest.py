import asyncio.subprocess
import sys

async def get_date():
    dirs = '/tmp /var /Users/tneale'.split()

    # Create the subprocess, redirect the standard output into a pipe
    for dr in dirs:
        proc = await asyncio.create_subprocess_exec('./lessdir.sh', dr, stdout)
        await proc.communicate()
        await asyncio.sleep(2)
        if proc.returncode is None:
            await proc.terminate()


loop = asyncio.get_event_loop()
date = loop.run_until_complete(get_date())
loop.close()
