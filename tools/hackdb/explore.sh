#!/bin/sh
set +e
UA="Mozilla/5.0 mixtape-hackdb"
curl -sSL -A "$UA" -o out/view2021.html "https://archive.org/download/rhdn-20210914/RHDN-20210914.zip/"
echo "view size $(wc -c < out/view2021.html)"
python3 - <<'PY' > out/list2021.txt
import re,html
s=open('out/view2021.html',encoding='utf-8',errors='replace').read()
rows=re.findall(r'<tr>(.*?)</tr>',s,flags=re.S)
for r in rows:
    cells=[html.unescape(re.sub(r'<[^>]+>','',c)).strip() for c in re.findall(r'<td[^>]*>(.*?)</td>',r,flags=re.S)]
    href=re.findall(r'href="([^"]+)"',r)
    if cells: print(' | '.join(cells), '|', href[:1])
PY
wc -l out/list2021.txt; head -40 out/list2021.txt
grep -i "hacks/" out/list2021.txt | head -20
grep -ic "pokemon" out/list2021.txt
