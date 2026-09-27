#!/bin/sh
# Builds the OnionOS package: dist/Mixtape-<version>-OnionOS.zip
set -e
cd "$(dirname "$0")"
VERSION=$(grep -o 'const version = "[^"]*"' main.go | cut -d'"' -f2)
go mod tidy
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o package/Mixtape/mixtape .
rm -rf dist && mkdir -p dist/stage/App
cp -r package/Mixtape dist/stage/App/
rm -rf dist/stage/App/Mixtape/data
(cd dist/stage && zip -qr "../Mixtape-v$VERSION-OnionOS.zip" App)
rm -rf dist/stage
echo "Built dist/Mixtape-v$VERSION-OnionOS.zip"
