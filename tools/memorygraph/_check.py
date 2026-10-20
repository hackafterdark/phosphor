import io, re
s = io.open('tools/memorygraph/_cosmos_check.js', encoding='utf-8').read()
for pat in ['inputMasses', 'pointMasses', 'setPointMasses', 'masses']:
    hits = re.findall(r'.{40}' + pat + r'.{40}', s)
    print(pat, len(hits))
    for h in hits[:2]: print('  ', repr(h))