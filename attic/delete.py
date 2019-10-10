#!/usr/bin/env python3.6
from elasticsearch import Elasticsearch


def main():
    es = Elasticsearch()
    es.indices.delete('totalrecall')


if __name__ == "__main__":
    main()
