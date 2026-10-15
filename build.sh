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
# Mixtape carries the theme, so its SELECT menu can install it (theme.go).
mkdir -p dist/stage/App/Mixtape/theme
cp -r theme/Mixtape dist/stage/App/Mixtape/theme/
(cd dist/stage && zip -qr "../Mixtape-v$VERSION-OnionOS.zip" App)
rm -rf dist/stage
# The OnionOS theme (theme/Mixtape, drawn by theme/generate.py). Its name
# leaves out "OnionOS" so Mixtape's self-update never mistakes it for the app.
mkdir -p dist/stage/Themes
cp -r theme/Mixtape dist/stage/Themes/
(cd dist/stage && zip -qr "../Mixtape-Theme-v$VERSION.zip" Themes)
rm -rf dist/stage
echo "Built dist/Mixtape-v$VERSION-OnionOS.zip and dist/Mixtape-Theme-v$VERSION.zip"
