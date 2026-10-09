"""New source-driven parameter combinations, original CLI and synthetic replies."""
import json
import comprehensive_lab as lab
SCHEMA={'type':'object','properties':{'ok':{'type':'boolean'}},'required':['ok'],'additionalProperties':False}
lab.CASES=[]
for model in ['claude-sonnet-4-6','claude-opus-5-5','claude-haiku-5-5']:
 prefix=model.replace('claude-','')
 lab.CASES.append({'name':prefix+'-schema-native','base':'api-arg','model':model,'args':['--json-schema',json.dumps(SCHEMA)]})
 for name,extra in [('format-extra',{'output_config':{'format':{'type':'json_schema','schema':SCHEMA}}}),('stop-extra',{'stop_sequences':['STOP_AUDIT']}),('cache-extra',{'cache_control':{'type':'ephemeral','ttl':'1h'}})]:
  lab.CASES.append({'name':prefix+'-'+name,'base':'api-arg','model':model,'env':{'CLAUDE_CODE_EXTRA_BODY':json.dumps(extra)},'generic_controls':extra})
 lab.CASES.append({'name':prefix+'-thinking-tool','base':'thinking-tool','model':model})
for extra in [{'thinking':{'type':'adaptive','display':'updates'},'output_config':{'effort':'low'}},{'max_tokens':1024,'output_config':{'effort':'xhigh'}},{'top_p':0.9}]:
 name=['updates-low','max1024-xhigh','top-p'][len(lab.CASES)-15]
 lab.CASES.append({'name':'haiku55-'+name,'base':'api-arg','model':'claude-haiku-5-5','env':{'CLAUDE_CODE_EXTRA_BODY':json.dumps(extra)},'generic_controls':extra})
if __name__=='__main__':lab.main()
