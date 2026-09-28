#!/bin/sh
set -e
go mod tidy
go test ./patch ./tools/hackdb
go run ./tools/hackdb -out out/hacks.json -report out/report.txt
go run ./tools/hackdb -verify out/hacks.json -report out/verify.txt
