#!/bin/sh
set -e

REPO="billmal071/lobster"

echo "🦞 Installing Lobster from $REPO..."

# Get latest release version
LATEST_RELEASE=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_RELEASE" ]; then
    echo "Error: Could not determine latest release."
    exit 1
fi

VERSION_NO_V=$(echo "$LATEST_RELEASE" | sed 's/^v//')
echo "Latest release: $LATEST_RELEASE"

# Determine architecture
ARCH=$(uname -m)
case $ARCH in
    x86_64) PKG_ARCH="amd64"; TAR_ARCH="x86_64" ;;
    aarch64) PKG_ARCH="arm64"; TAR_ARCH="arm64" ;;
    armv7l) PKG_ARCH="armv7"; TAR_ARCH="armv7" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

OS_LOWER=$(uname -s | tr '[:upper:]' '[:lower:]')
OS_TITLE=$(uname -s | awk '{print toupper(substr($0,1,1)) tolower(substr($0,2))}')

# Downloading function
download() {
    URL="https://github.com/billmal071/lobster/releases/download/$LATEST_RELEASE/$1"
    echo "Downloading $URL..."
    if ! curl -sLf "$URL" -o "/tmp/$1"; then
        echo "Error: Failed to download $URL"
        exit 1
    fi
}

echo "Detected OS: $OS_TITLE, Architecture: $ARCH"

if [ "$OS_LOWER" = "linux" ]; then
    if command -v dpkg >/dev/null 2>&1; then
        # Debian/Ubuntu
        FILE="lobster_${VERSION_NO_V}_linux_${PKG_ARCH}.deb"
        download "$FILE"
        echo "Installing $FILE via dpkg..."
        sudo dpkg -i "/tmp/$FILE" || true
        # Install missing dependencies (e.g., fzf) automatically
        sudo apt-get install -f -y
    elif command -v rpm >/dev/null 2>&1; then
        # RHEL/Fedora/CentOS
        FILE="lobster_${VERSION_NO_V}_linux_${PKG_ARCH}.rpm"
        download "$FILE"
        echo "Installing $FILE via rpm..."
        if command -v dnf >/dev/null 2>&1; then
            sudo dnf install -y "/tmp/$FILE"
        elif command -v yum >/dev/null 2>&1; then
            sudo yum install -y "/tmp/$FILE"
        else
            sudo rpm -i "/tmp/$FILE"
        fi
    else
        # Fallback to tarball
        FILE="lobster_${OS_TITLE}_${TAR_ARCH}.tar.gz"
        download "$FILE"
        echo "Extracting binary..."
        tar -xzf "/tmp/$FILE" -C /tmp lobster
        sudo install -m 755 /tmp/lobster /usr/local/bin/lobster
    fi
elif [ "$OS_LOWER" = "darwin" ]; then
    FILE="lobster_${OS_TITLE}_${TAR_ARCH}.tar.gz"
    download "$FILE"
    echo "Extracting binary..."
    tar -xzf "/tmp/$FILE" -C /tmp lobster
    sudo install -m 755 /tmp/lobster /usr/local/bin/lobster
else
    echo "Unsupported OS: $OS_LOWER"
    exit 1
fi

echo "✅ Lobster installed successfully!"
echo "Run 'lobster' in your terminal to get started."
