#!/bin/sh
set -e
go mod tidy
go test ./patch ./tools/hackdb
go run ./tools/hackdb -out out/hacks.json -report out/report.txt
