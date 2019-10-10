#!/usr/bin/env python3.6
from elasticsearch import Elasticsearch

es = Elasticsearch()


index = es.indices.get_mapping('totalrecall')
mappings = index['totalrecall']
props = mappings['mappings']['event']['properties']
env = props['env']['properties']

for key, attrs in env.items():
    attrs['index'] = 'not_analyzed'

es.indices.delete('totalrecall')

import pprint
pprint.pprint(mappings)

import requests
#import pdb; pdb.set_trace()
resp = requests.put('http://localhost:9200/totalrecall', json=mappings)
print(resp)
print(resp.json())


#es.indices.put_mapping(index='totalrecall', doc_type='event', body=dict(properties=props))
#import pdb; pdb.set_trace()

