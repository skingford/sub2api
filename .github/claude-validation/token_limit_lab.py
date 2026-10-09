import json
import comprehensive_lab as lab
lab.CASES=[{'name':model+'-max64','base':'api-arg','model':model,'env':{'CLAUDE_CODE_EXTRA_BODY':json.dumps({'max_tokens':64})},'generic_controls':{'max_tokens':64}} for model in ['claude-sonnet-4-6','claude-opus-5-5','claude-haiku-5-5']]
if __name__=='__main__':lab.main()
