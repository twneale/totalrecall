#!/usr/bin/env python3.6

import sys
import re
import subprocess


def main():
    rgxs = (
        r'^ +(-+[\w\-]+)',
        r'--[\w\-]+'
    )
    if 1 < len(sys.argv):
        man = str(subprocess.check_output('man %s | col -b' % sys.argv[1], shell=True), 'utf8')
    else:
        man = sys.stdin.read()
    flags = set()
    for rgx in rgxs:
        for match in re.findall(rgx, man, re.MULTILINE):
            flags.add(match)
    for flag in sorted(flags):
        print(flag)

if __name__ == "__main__":
    main()
