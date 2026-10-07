import collections
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT=Path('/evidence')
CASES = [('', name) for name in (sys.argv[1:] or ['baseline','read-tool','retry-503','multi-turn','oauth-synthetic','unicode','count-context'])]
results=[]
for parent,name in CASES:
    folder=ROOT/parent/name
    records=json.loads((folder/'requests.json').read_text())
    actual=[r for r in records if r['method']=='POST' and r['path'].startswith('/v1/messages')]
    args=['tshark','-r',str(folder/'capture.pcap'),'-o','tls.keylog_file:'+str(folder/'server-reference.keys')]
    packets=json.loads(subprocess.check_output(args+['-Y','http.request','-T','json','-x'],stderr=subprocess.DEVNULL))
    decoded=[]
    for packet in packets:
        layers=packet['_source']['layers']
        http=layers.get('http',{})
        uri=next((v['http.request.uri'] for v in http.values() if isinstance(v,dict) and 'http.request.uri' in v),None)
        if not uri or not uri.startswith('/v1/messages'):
            continue
        body=bytes.fromhex(http['http.file_data_raw'][0])
        decoded.append({'path':uri,'sha256':hashlib.sha256(body).hexdigest(),
                        'stream':layers['tcp']['tcp.stream']})
    expected=collections.Counter((r['path'],r['raw_body_sha256']) for r in actual)
    observed=collections.Counter((r['path'],r['sha256']) for r in decoded)
    dropped=int(re.search(r'(\d+) packets dropped by kernel',(folder/'tcpdump.log').read_text()).group(1))
    fields=['tcp.stream','tls.handshake.ja3','tls.handshake.ja3_full','tls.handshake.extensions_alpn_str',
            'tls.handshake.ciphersuite','tls.handshake.extension.type']
    command=args+['-Y','tls.handshake.type == 1','-T','fields']
    for field in fields: command += ['-e',field]
    hellos=[]
    for line in subprocess.check_output(command,stderr=subprocess.DEVNULL,text=True).splitlines():
        hello=dict(zip(fields,line.split('\t')))
        if hello['tcp.stream'] in {d['stream'] for d in decoded}: hellos.append(hello)
    item={'case':parent+'/'+name,'request_count':len(actual),'decrypted_count':len(decoded),
          'all_bodies_match':expected==observed,'kernel_dropped_packets':dropped,'client_hellos':hellos,
          'header_order':[list(r['headers']) for r in actual],
          'negotiated':[{'tls':r['tls_version'],'alpn':r['alpn'],'cipher':r['cipher']} for r in actual]}
    results.append(item)
    assert item['all_bodies_match'] and dropped==0, item
Path('/analysis/capture-validation.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps([{'case':r['case'],'requests':r['request_count'],'verified':r['all_bodies_match'],
                   'ja3':sorted({h['tls.handshake.ja3'] for h in r['client_hellos']})} for r in results],indent=2))
