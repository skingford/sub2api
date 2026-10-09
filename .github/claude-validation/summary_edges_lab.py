"""Probe source-defined summary whitespace with the unmodified native CLI."""
import comprehensive_lab as lab
lab.CASES=[{'name':'summary-'+name,'base':'compact','model':'claude-sonnet-4-6','summary_pad':pad} for name,pad in [('space',' '),('bom','\ufeff'),('nel','\u0085')]]
original_dumps=lab.json.dumps
def dumps(obj,*args,**kwargs):
 if isinstance(obj,dict) and obj.get('type')=='content_block_delta':
  delta=obj.get('delta',{})
  text=delta.get('text','')
  if delta.get('type')=='text_delta' and '<summary>' in text:
   pad=lab.CURRENT['summary_pad'];obj=dict(obj);obj['delta']=dict(delta,text=text.replace('<summary>\n','<summary>\n'+pad).replace('\n</summary>',pad+'\n</summary>'))
 return original_dumps(obj,*args,**kwargs)
if __name__=='__main__':
 lab.json.dumps=dumps
 lab.main()
