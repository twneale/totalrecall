#!/bin/bash

mkdir certs

cat > certs/server-config.cnf << EOF
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req
prompt = no

[req_distinguished_name]
CN = fluentbit

[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
subjectAltName = @alt_names

[alt_names]
IP.1 = 127.0.0.1
DNS.1 = localhost
DNS.2 = fluentbit
DNS.3 = haproxy
EOF

# Create the CA certificate
openssl genrsa -out certs/ca.key 4096
openssl req -new -x509 -days 365 -key certs/ca.key -out certs/ca.crt -subj "/CN=TotalRecall"

# Create server certificate
openssl genrsa -out certs/server.key 2048
openssl req -new -key certs/server.key -out certs/server.csr -config ./certs/server-config.cnf
openssl x509 -req -days 365 -in certs/server.csr -CA certs/ca.crt -CAkey certs/ca.key -CAcreateserial -out certs/server.crt -extensions v3_req -extfile ./certs/server-config.cnf

# Create client certificate for Fluent Bit
openssl genrsa -out certs/client.key 2048
openssl req -new -key certs/client.key -out certs/client.csr -subj "/CN=TotalRecallClient"
openssl x509 -req -days 365 -in certs/client.csr -CA certs/ca.crt -CAkey certs/ca.key -CAcreateserial -out certs/client.crt

# Create PEM file for HAProxy (combines cert and key)
cat certs/server.crt certs/server.key > certs/server.pem

# Set appropriate permissions
chmod 600 certs/*.key certs/*.pem

mkdir -p ~/.totalrecall/
chmod 700 ~/.totalrecall/
cp -f ./certs/client.* ~/.totalrecall/
cp -f ./certs/ca.crt ~/.totalrecall/

