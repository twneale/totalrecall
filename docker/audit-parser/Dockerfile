FROM python:3.6

RUN pip install lxml click
COPY audit-parser /opt/audit-parser 
CWD /opt/audit-parser
ENTRYPOINT ["./main.py", "--exclude-argv", "exclude/argv"]
