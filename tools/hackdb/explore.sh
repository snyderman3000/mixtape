#!/bin/sh
set +e
UA="Mozilla/5.0 mixtape-hackdb"
B="https://archive.org/download/romhackingbackup08102024/www.romhacking.net.zip/www.romhacking.net"
mkdir -p out/pages
for id in 1 2425 3456; do curl -sSL -A "$UA" -o out/pages/hack$id.html "$B/hacks/$id/index.html"; done
curl -sSL -A "$UA" -o out/pages/dl1.html "$B/download/hacks/1/index.html"; echo "dl1 $(wc -c < out/pages/dl1.html)"
curl -sSL -A "$UA" -o out/pages/hacklist.html "$B/hacks/index.html"; echo "list $(wc -c < out/pages/hacklist.html)"
# S3 bucket reachable?
curl -sSL -A "$UA" -o /dev/null -w "s3 img http=%{http_code}\n" "https://s3-external-1.amazonaws.com/romhacking-hacks/hacks/nes/images/titles/1titlescreen.png"
curl -sSL -A "$UA" -w "\ns3 list http=%{http_code}\n" "https://s3-external-1.amazonaws.com/romhacking-hacks/?max-keys=5" | head -c 1500; echo
# older full archive: listing head
curl -sSL -A "$UA" "https://archive.org/download/rhdn-20210914/RHDN-20210914.zip/" | grep -o 'href="[^"]*"' | head -60
