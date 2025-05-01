#!/bin/bash
# Updated script to import client certificates into macOS Keychain

# Default paths
CA_CERT="${CA_CERT:-certs/ca.crt}"
CLIENT_CERT="${CLIENT_CERT:-certs/client.crt}"
CLIENT_KEY="${CLIENT_KEY:-certs/client.key}"
P12_OUTPUT="${P12_OUTPUT:-certs/client.p12}"
KEYCHAIN="${KEYCHAIN:-login.keychain}"
P12_PASSWORD="temppass123"  # Temporary password for PKCS#12 file

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --ca)
      CA_CERT="$2"
      shift 2
      ;;
    --cert)
      CLIENT_CERT="$2"
      shift 2
      ;;
    --key)
      CLIENT_KEY="$2"
      shift 2
      ;;
    --keychain)
      KEYCHAIN="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1"
      echo "Usage: $0 [--ca path/to/ca.crt] [--cert path/to/client.crt] [--key path/to/client.key] [--keychain name.keychain]"
      exit 1
      ;;
  esac
done

# Check if files exist
for file in "$CA_CERT" "$CLIENT_CERT" "$CLIENT_KEY"; do
  if [[ ! -f "$file" ]]; then
    echo "Error: File $file does not exist!"
    exit 1
  fi
done

echo "=== Importing certificates into macOS Keychain ==="
echo "CA Certificate: $CA_CERT"
echo "Client Certificate: $CLIENT_CERT"
echo "Client Key: $CLIENT_KEY"
echo "Keychain: $KEYCHAIN"
echo ""

# Import CA certificate
echo "Importing CA certificate..."
security import "$CA_CERT" -k "$KEYCHAIN" -t cert -A
if [[ $? -ne 0 ]]; then
  echo "Error: Failed to import CA certificate!"
  exit 1
fi

# Alternative approach - don't use PKCS#12, import cert and key separately
echo "Importing client certificate..."
security import "$CLIENT_CERT" -k "$KEYCHAIN" -t cert -A
if [[ $? -ne 0 ]]; then
  echo "Error: Failed to import client certificate!"
  exit 1
fi

# Check certificate format and verify key matches certificate
echo "Verifying key matches certificate..."
CERT_MODULUS=$(openssl x509 -noout -modulus -in "$CLIENT_CERT" | openssl md5)
KEY_MODULUS=$(openssl rsa -noout -modulus -in "$CLIENT_KEY" | openssl md5)

if [[ "$CERT_MODULUS" != "$KEY_MODULUS" ]]; then
  echo "Error: Certificate and key do not match!"
  exit 1
fi

# Try to import the key directly
echo "Importing client key..."
security import "$CLIENT_KEY" -k "$KEYCHAIN" -t priv -A
if [[ $? -ne 0 ]]; then
  echo "Warning: Direct key import failed, trying alternative method..."
  
  # Create a temporary PKCS#12 file with explicit format control
  echo "Creating temporary PKCS#12 file with stronger settings..."
  openssl pkcs12 -export -out "$P12_OUTPUT" \
    -inkey "$CLIENT_KEY" \
    -in "$CLIENT_CERT" \
    -certfile "$CA_CERT" \
    -passin pass:"" \
    -passout pass:"$P12_PASSWORD" \
    -macalg sha256 \
    -keypbe AES-256-CBC \
    -certpbe AES-256-CBC
  
  if [[ $? -ne 0 ]]; then
    echo "Error: Failed to create PKCS#12 file!"
    exit 1
  fi
  
  # Import with explicit password format
  echo "Importing from PKCS#12 file..."
  security import "$P12_OUTPUT" -k "$KEYCHAIN" -P "$P12_PASSWORD" -A -x
  
  if [[ $? -ne 0 ]]; then
    echo "Error: PKCS#12 import failed."
    echo "Try manually importing with Keychain Access app:"
    echo "1. Open Keychain Access"
    echo "2. Select File > Import Items"
    echo "3. Browse to $P12_OUTPUT"
    echo "4. Enter the password: $P12_PASSWORD"
    rm -f "$P12_OUTPUT"
    exit 1
  fi
  
  # Remove temporary PKCS#12 file
  rm -f "$P12_OUTPUT"
fi

# Set trust settings for CA certificate
echo "Setting trust settings for CA certificate..."
security add-trusted-cert -d -r trustRoot -k "$KEYCHAIN" "$CA_CERT"
if [[ $? -ne 0 ]]; then
  echo "Warning: Failed to set trust settings for CA certificate."
  echo "You may need to manually trust this certificate in Keychain Access."
fi

echo ""
echo "=== Import completed successfully ==="
echo "Certificates have been imported into your $KEYCHAIN keychain."
echo ""
echo "To verify the certificates were imported correctly, you can run:"
echo "  security find-certificate -a -c \"$(openssl x509 -noout -subject -in \"$CLIENT_CERT\" | sed 's/^subject=//g')\" -Z | grep SHA-1"
echo ""
echo "You might need to restart applications that use these certificates."
