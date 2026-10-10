import json,runpy
from pathlib import Path
import lab
headers=[]
original=lab.Handler.respond
def respond(self,*args,**kwargs):
 headers.append(list(self.headers.items()))
 return original(self,*args,**kwargs)
lab.Handler.respond=respond
result=runpy.run_path('/audit/tool_constraint_wire_lab.py',run_name='__main__')
records=result['records']
assert len(records)==len(headers)
for row,h in zip(records,headers):row['headers_ordered']=h
Path('/work/records-with-headers.json').write_text(json.dumps(records,indent=2)+'\n')
