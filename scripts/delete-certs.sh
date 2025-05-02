#!/bin/bash
# Script to delete certificates from macOS Keychain

# Default paths
CA_CERT="${CA_CERT:-certs/ca.crt}"
CLIENT_CERT="${CLIENT_CERT:-certs/client.crt}"
KEYCHAIN="${KEYCHAIN:-login.keychain}"

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
    --keychain)
      KEYCHAIN="$2"
      shift 2
      ;;
    --all)
      DELETE_ALL=true
      shift
      ;;
    *)
      echo "Unknown option: $1"
      echo "Usage: $0 [--ca path/to/ca.crt] [--cert path/to/client.crt] [--keychain name.keychain] [--all]"
      exit 1
      ;;
  esac
done

# Check if files exist when not using --all
if [[ -z "$DELETE_ALL" ]]; then
  for file in "$CA_CERT" "$CLIENT_CERT"; do
    if [[ ! -f "$file" ]]; then
      echo "Error: File $file does not exist!"
      exit 1
    fi
  done
fi

echo "=== Deleting certificates from macOS Keychain ==="
echo "Keychain: $KEYCHAIN"
echo ""

# Function to delete certificate by SHA-1 hash
delete_cert_by_hash() {
  local hash="$1"
  echo "Deleting certificate with hash: $hash"
  # Corrected syntax - keychain as positional argument
  security delete-certificate -Z "$hash" -t "$KEYCHAIN"
  return $?
}

# Function to delete certificate by common name
delete_cert_by_name() {
  local name="$1"
  echo "Deleting certificate with name: $name"
  # Corrected syntax - keychain as positional argument
  security delete-certificate -c "$name" -t "$KEYCHAIN"
  return $?
}

# Function to extract SHA-1 hash from certificate file
get_cert_hash() {
  local cert_file="$1"
  # Get certificate hash (SHA-1 fingerprint)
  openssl x509 -noout -fingerprint -sha1 -in "$cert_file" | cut -d= -f2 | tr -d ':'
}

# Function to get common name from certificate file
get_cert_common_name() {
  local cert_file="$1"
  # Extract just the common name (CN) from the subject
  openssl x509 -noout -subject -in "$cert_file" | sed -n 's/.*CN *= *\([^,]*\).*/\1/p'
}

if [[ "$DELETE_ALL" == true ]]; then
  echo "Looking for certificates with names related to your mTLS setup..."
  
  # Try to find and delete certificates with common mTLS names
  for name in "CA" "Certificate Authority" "client" "fluentbit" "haproxy" "elasticsearch"; do
    echo "Attempting to delete certificates with name containing '$name'..."
    delete_cert_by_name "$name"
  done
  
  echo "Note: This may not have removed all certificates. Check Keychain Access app to verify."
else
  # Delete CA certificate
  echo "Deleting CA certificate..."
  ca_hash=$(get_cert_hash "$CA_CERT")
  ca_name=$(get_cert_common_name "$CA_CERT")
  
  echo "Attempting to delete by hash: $ca_hash"
  delete_cert_by_hash "$ca_hash"
  
  echo "Attempting to delete by name: $ca_name"
  delete_cert_by_name "$ca_name"
  
  # Delete client certificate
  echo "Deleting client certificate..."
  client_hash=$(get_cert_hash "$CLIENT_CERT")
  client_name=$(get_cert_common_name "$CLIENT_CERT")
  
  echo "Attempting to delete by hash: $client_hash"
  delete_cert_by_hash "$client_hash"
  
  echo "Attempting to delete by name: $client_name"
  delete_cert_by_name "$client_name"
fi

echo ""
echo "=== Deletion completed ==="
echo "To verify all certificates were removed correctly, you can:"
echo "  1. Open Keychain Access app"
echo "  2. Select the $KEYCHAIN keychain"
echo "  3. Search for the certificate names or subjects"
echo ""
echo "You might need to restart applications that use these certificates."
