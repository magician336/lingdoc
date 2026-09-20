"""Validate design examples and the reference trace; does not run the application."""
import copy
import hashlib
import json
import re
import warnings
from pathlib import Path

warnings.filterwarnings('ignore', category=DeprecationWarning)
from jsonschema import Draft7Validator, FormatChecker, RefResolver

ROOT=Path(__file__).resolve().parent
def read(name): return json.loads((ROOT/name).read_text(encoding='utf-8'))
spec=read('openapi.json')
cases=read('scenarios.json')
flow=read('workflow.json')
def canon(x): return json.dumps(x,ensure_ascii=False,sort_keys=True,separators=(',',':')).encode('utf-8')
def sha(x): return hashlib.sha256(canon(x)).hexdigest()
def ptr(root,p):
    x=root
    for key in p.lstrip('#').lstrip('/').split('/'):
        x=x[int(key)] if isinstance(x,list) else x[key.replace('~1','/').replace('~0','~')]
    return x
refs=0
def walk(x):
    global refs
    if isinstance(x,dict):
        if '$ref' in x:
            assert x['$ref'].startswith('#/')
            ptr(spec,x['$ref']); refs+=1
        for value in x.values(): walk(value)
    elif isinstance(x,list):
        for value in x: walk(value)
walk(spec)
def normalize(x):
    if isinstance(x,list): return [normalize(v) for v in x]
    if not isinstance(x,dict): return x
    result={k:normalize(v) for k,v in x.items() if k!='nullable'}
    if x.get('nullable'):
        assert isinstance(result.get('type'),str)
        result['type']=[result['type'],'null']
    return result
normalized=normalize(spec)
resolver=RefResolver.from_schema(normalized)
checks=0
def validate(schema,value,label):
    global checks
    errors=list(Draft7Validator(normalize(schema),resolver=resolver,format_checker=FormatChecker()).iter_errors(value))
    assert not errors, label+': '+str(errors[0]) if errors else label
    checks+=1
for schema in normalized['components']['schemas'].values(): Draft7Validator.check_schema(schema)
ops={}
for path,item in spec['paths'].items():
    for method,op in item.items():
        assert method in {'get','post','put','patch','delete'}
        oid=op['operationId']; assert oid not in ops
        ops[oid]=op
        assert set(re.findall(r'\{([^}]+)\}',path))=={p['name'] for p in op.get('parameters',[]) if p['in']=='path' and p['required']}
        if 'requestBody' in op:
            media=op['requestBody']['content']['application/json']
            validate(media['schema'],media['example'],oid+' request')
        for code,response in op['responses'].items():
            for media in response.get('content',{}).values():
                for name,example in media.get('examples',{}).items():
                    validate(media['schema'],example['value'],oid+' '+code+' '+name)

scenario_steps=0
for scenario in cases['scenarios']:
    for step in scenario.get('steps',[]):
        response=ops[step['operation_id']]['responses'][str(step['expected_http'])]
        if 'response_example' in step:
            assert step['response_example'] in response['content']['application/json']['examples'],step
        scenario_steps+=1

def expand(x,env):
    if isinstance(x,dict): return {k:expand(v,env) for k,v in x.items()}
    if isinstance(x,list): return [expand(v,env) for v in x]
    if not isinstance(x,str): return x
    match=re.fullmatch(r'\{\{(\w+)\}\}',x)
    if match: return env[match[1]]
    return re.sub(r'\{\{(\w+)\}\}',lambda m:str(env[m[1]]),x)
env={};resolved=[]
for step in flow['steps']:
    op=ops[step['operation_id']]
    request=expand(step['request'],env)
    for p in op.get('parameters',[]):
        bucket=request['path_params'] if p['in']=='path' else request['headers'] if p['in']=='header' else {}
        if p.get('required'): assert p['name'] in bucket,(step['id'],p)
        if p['name'] in bucket: validate(p['schema'],bucket[p['name']],step['id']+' param')
    if 'requestBody' in op:
        validate(op['requestBody']['content']['application/json']['schema'],request['json'],step['id']+' request')
    if 'reference_response' in step:
        response=op['responses'][str(step['expected_http'])]['content']['application/json']
        validate(response['schema'],step['reference_response'],step['id']+' response')
        for var,p in step['capture'].items(): env[var]=ptr(step['reference_response'],p)
    resolved.append({**step,'request':request})

def unique_by(items,key):
    keys=[item[key] for item in items]
    assert len(keys)==len(set(keys)),'duplicate '+key
    return set(keys)
def review_ids(x): return unique_by(x['review_items'],'id')
def asset_pairs(items):
    unique_by(items,'asset_id')
    return {(v['asset_id'],v['asset_revision']) for v in items}
def check_frozen(snapshot):
    frozen=snapshot['frozen_input']
    assert snapshot['snapshot_digest']==sha(frozen),'digest mismatch'
    assert snapshot['check']['project_version']==frozen['project_version']
    assert snapshot['check']['ruleset_hash']==frozen['template']['ruleset_hash']
    assert frozen['template']['ruleset_hash']==sha(sorted(frozen['template']['rules'],key=lambda r:r['rule_id']))
    assert snapshot['project_id']==frozen['project_id']
    unique_by(frozen['sources'],'id');unique_by(frozen['chapters'],'chapter_id')
    sources={s['id']:s for s in frozen['sources']}
    frozen_assets=asset_pairs(frozen['asset_versions'])
    for source in sources.values():
        assert source['project_id']==frozen['project_id']
        assert (source['asset_id'],source['asset_revision']) in frozen_assets
        assert source['asset_id'] in frozen['policy_asset_ids']
        assert source['quoted_text_hash']==hashlib.sha256(source['quoted_text'].encode('utf-8')).hexdigest()
    for ch in frozen['chapters']:
        expected_review_ids=review_ids(ch)
        assert set(re.findall(r'\[\[source:([^\]]+)\]\]',ch['body_markdown']))==set(ch['source_ids'])
        assert set(ch['source_ids'])<=set(sources)
        if snapshot['check']['status']=='passed':
            assert ch['chapter_version_id'] and ch['body_markdown'].strip()
            c=ch['confirmation']
            assert c['chapter_id']==ch['chapter_id'],'confirmation chapter mismatch'
            assert c['chapter_version_id']==ch['chapter_version_id'] and c['spec_revision']==frozen['spec_revision']
            assert c['template_version']==frozen['template']['version'],'confirmation template mismatch'
            expected_assets={(sources[s]['asset_id'],sources[s]['asset_revision']) for s in ch['source_ids']}
            assert asset_pairs(c['asset_versions'])==expected_assets,'confirmation asset versions mismatch'
            assert unique_by(c['review_decisions'],'review_item_id')==expected_review_ids

def check_acceptance(previous,candidate,request,result):
    assert request['candidate_id']==candidate['id']
    assert request['expected_chapter_version_id']==previous['current_version_id']
    assert request['expected_spec_revision']==candidate['basis']['spec_revision']
    assert candidate['basis']['chapter_version_id']==previous['current_version_id']
    assert candidate['validity']=='fresh'
    assert candidate['project_id']==previous['project_id']==result['project_id']
    assert candidate['chapter_id']==previous['id']==result['id']
    if previous['current_version_id'] is not None: assert request['replace_existing'] is True,'replacement not acknowledged'
    else: assert request['replace_existing'] is False
    for field in ['body_markdown','source_ids','review_items']:
        assert result[field]==candidate[field],'replacement must exactly use candidate '+field
    review_ids(result)
    assert result['current_version_id'] and result['current_version_id']!=previous['current_version_id']
    assert result['confirmation_valid'] is False

def check_trace(steps):
    project_version=None;spec_revision=None;chapters={};candidate=None;seen={};confirmations={}
    for step in steps:
        oid=step['operation_id'];req=step['request'];body=req['json']
        response=step.get('reference_response');data=response['data'] if response else None
        key=req['headers'].get('Idempotency-Key')
        if key:
            signature=(step['actor'],oid,canon(req['path_params']),key)
            if signature in seen:
                prior=seen[signature]
                assert prior==(body,data),'replay mismatch'
                assert response['meta']=={'replayed':True,'refresh_required':True}
                continue
            seen[signature]=(copy.deepcopy(body),copy.deepcopy(data))
        if oid=='createProject':
            assert data['project_version']==1 and data['spec_revision']==0 and data['status']=='draft'
            project_version=1;spec_revision=0
        elif oid=='saveSpec':
            assert body['expected_spec_revision']==spec_revision
            spec_revision+=1;project_version+=1
            assert data['spec_revision']==spec_revision and data['project_version']==project_version
        elif oid=='activateProject':
            assert body['expected_spec_revision']==spec_revision
            project_version+=1
            assert data['project_version']==project_version and data['status']=='active'
        elif oid=='listChapters': chapters={c['id']:copy.deepcopy(c) for c in data}
        elif oid=='startGeneration':
            assert body['expected_spec_revision']==spec_revision
            assert body['expected_chapter_version_id']==chapters[body['chapter_id']]['current_version_id']
        elif oid=='getCandidate': candidate=copy.deepcopy(data)
        elif oid in {'acceptCandidate','saveChapter'}:
            previous=chapters[req['path_params']['chapterId']]
            assert body['expected_chapter_version_id']==previous['current_version_id'],'chapter CAS mismatch'
            assert body['expected_spec_revision']==spec_revision
            expected=candidate['review_items'] if oid=='acceptCandidate' else previous['review_items']
            assert data['review_items']==expected,'review lost'
            assert not data['confirmation_valid']
            if oid=='acceptCandidate': check_acceptance(previous,candidate,body,data)
            if oid=='saveChapter': assert body['body_markdown']==data['body_markdown']
            chapters[data['id']]=copy.deepcopy(data);project_version+=1
        elif oid=='confirmChapter':
            ch=chapters[req['path_params']['chapterId']]
            assert body['chapter_version_id']==ch['current_version_id'] and body['expected_spec_revision']==spec_revision
            assert unique_by(body['review_decisions'],'review_item_id')==review_ids(ch)
            assert data['review_decisions']==body['review_decisions']
            confirmations[ch['id']]={k:copy.deepcopy(v) for k,v in data.items() if k!='valid'}
            ch['confirmation_valid']=True;project_version+=1
        elif oid=='getProject': assert data['project_version']==project_version and data['spec_revision']==spec_revision
        elif oid=='prepareRelease':
            assert body['expected_project_version']==project_version
            assert data['frozen_input']['project_version']==project_version
            assert len(data['frozen_input']['chapters'])==len(chapters)==2
            for ch in data['frozen_input']['chapters']:
                latest=chapters[ch['chapter_id']]
                assert ch['chapter_version_id']==latest['current_version_id']
                assert ch['review_items']==latest['review_items'] and ch['body_markdown']==latest['body_markdown']
                assert ch['confirmation']==confirmations[ch['chapter_id']],'frozen confirmation differs from actual confirmation'
            check_frozen(data)
    assert project_version==8 and spec_revision==1

check_trace(resolved)
release_examples=ops['prepareRelease']['responses']['201']['content']['application/json']['examples']
for example in release_examples.values(): check_frozen(example['value']['data'])
assert all(c['chapter_version_id'] is None for c in release_examples['empty_chapters']['value']['data']['frozen_input']['chapters'])
canonical=(ROOT/'frozen-input.canonical.json').read_bytes()
assert canonical==canon(cases['canonical_fixture']['release']['frozen_input'])
assert hashlib.sha256(canonical).hexdigest()==(ROOT/'frozen-input.sha256').read_text().strip()

negative_results=[]
for label,index,mutate in [
 ('reject_lost_review_item',12,lambda s:s['reference_response']['data'].update(review_items=[])),
 ('reject_wrong_expected_version',12,lambda s:s['request']['json'].update(expected_chapter_version_id='wrong')),
 ('reject_frozen_content_changed_without_digest',18,lambda s:s['reference_response']['data']['frozen_input']['spec'].update(research_goal='changed')),
]:
    bad=copy.deepcopy(resolved);mutate(bad[index])
    try: check_trace(bad)
    except (AssertionError,KeyError): negative_results.append(label)
    else: raise AssertionError('negative case unexpectedly passed: '+label)

def must_reject(label,action):
    try: action()
    except (AssertionError,KeyError): negative_results.append(label)
    else: raise AssertionError('negative case unexpectedly passed: '+label)

for label,mutate in [
 ('confirmation_wrong_chapter',lambda c:c.update(chapter_id='wrong-chapter')),
 ('confirmation_wrong_template',lambda c:c.update(template_version='wrong-template')),
 ('confirmation_missing_asset_versions',lambda c:c.update(asset_versions=[])),
 ('duplicate_review_decision',lambda c:c['review_decisions'].append(copy.deepcopy(c['review_decisions'][0]))),
 ('same_review_id_different_reason',lambda c:c['review_decisions'].append({**c['review_decisions'][0],'reason':'different'})),
]:
    bad=copy.deepcopy(cases['canonical_fixture']['release'])
    mutate(bad['frozen_input']['chapters'][0]['confirmation'])
    bad['snapshot_digest']=sha(bad['frozen_input'])
    must_reject(label,lambda:check_frozen(bad))
bad=copy.deepcopy(cases['canonical_fixture']['release'])
bad['frozen_input']['chapters'][0]['review_items'].append({**bad['frozen_input']['chapters'][0]['review_items'][0],'statement':'different'})
bad['snapshot_digest']=sha(bad['frozen_input'])
must_reject('same_review_item_id_different_statement',lambda:check_frozen(bad))
badtrace=copy.deepcopy(resolved)
badtrace[15]['reference_response']['data']['id']='different-confirmation-returned'
must_reject('frozen_confirmation_not_actual_return',lambda:check_trace(badtrace))

f20=next(c for c in cases['scenarios'] if c['id']=='F20')['reference']
for schema,key in [('Chapter','previous_chapter'),('Candidate','candidate'),('AcceptCandidate','request'),('Chapter','result')]:
    validate({'$ref':'#/components/schemas/'+schema},f20[key],'F20 '+key)
def check_reacceptance(record):
    check_acceptance(record['previous_chapter'],record['candidate'],record['request'],record['result'])
    assert record['archived_chapter']==record['previous_chapter'],'old chapter changed'
    assert record['archived_confirmation']==record['previous_confirmation'],'old confirmation changed'
check_reacceptance(f20)
for label,mutate in [
 ('replacement_without_acknowledgement',lambda x:x['request'].update(replace_existing=False)),
 ('replacement_silently_merges_old_body',lambda x:x['result'].update(body_markdown=x['previous_chapter']['body_markdown']+x['candidate']['body_markdown'])),
 ('replacement_erases_old_review_history',lambda x:x['archived_chapter'].update(review_items=[])),
]:
    bad=copy.deepcopy(f20);mutate(bad)
    must_reject(label,lambda:check_reacceptance(bad))

f21=next(c for c in cases['scenarios'] if c['id']=='F21')['reference']
def check_history(record):
    assert record['ui_query_order']==['getExport','getRelease']
    assert record['export_before']==record['export_after']
    a=record['release_before'];b=record['release_after']
    assert record['export_after']['snapshot_id']==b['id']
    assert a['is_current'] is True and b['is_current'] is False
    assert a['frozen_input']==b['frozen_input'] and a['snapshot_digest']==b['snapshot_digest']
    assert record['expected_label_after']=='历史版本'
    check_frozen(a);check_frozen(b)
check_history(f21)
for label,mutate in [
 ('history_label_without_release_read',lambda x:x.update(ui_query_order=['getExport'])),
 ('history_label_claims_current',lambda x:x.update(expected_label_after='当前内容')),
]:
    bad=copy.deepcopy(f21);mutate(bad)
    must_reject(label,lambda:check_history(bad))

report={'result':'PASS','scope':'static_design_artifacts_and_reference_trace_only','paths':len(spec['paths']),
 'operations':len(ops),'core_operations':sum(o['x-delivery-phase']=='core' for o in ops.values()),
 'schemas':len(spec['components']['schemas']),'local_refs':refs,'shape_checks':checks,
 'scenarios':len(cases['scenarios']),'scenario_example_references':scenario_steps,'continuous_trace_steps':len(resolved),
 'negative_static_cases_rejected':negative_results,
 'not_run':['complete OpenAPI meta-schema validation','Prism runtime','real provider/database/model tests','actual DOCX export/opening'],
 'note':'This validates a proposed trace, not implemented server behavior. Export hash remains an explicit placeholder in examples.'}
(ROOT/'validation-result.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(report,ensure_ascii=False))
