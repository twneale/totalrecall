# Create the files as shown above
# - Copy dirjump.go to this directory
# - Copy go.mod to this directory

# Build and install
go mod tidy
go build -o dirjump
sudo mv dirjump /usr/local/bin

# Create the wrapper script
# - Copy dirjump.sh to a location of your choice
chmod +x dirjump.sh
sudo mv dirjump.sh /usr/local/bin/

