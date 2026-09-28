#!/bin/sh
# Rebuild catalog/hacks.json from the RomHacking.net archive, then verify every entry.
set -e
mkdir -p out
go run ./tools/hackdb -out out/hacks.json -report out/report.txt
go run ./tools/hackdb -verify out/hacks.json -report out/verify.txt
