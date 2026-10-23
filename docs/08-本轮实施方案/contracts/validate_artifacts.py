"""Validate design examples and the reference trace; does not run the application."""
import argparse
import copy
import importlib.util
import hashlib
import json
import re
import sys
import warnings
from pathlib import Path

warnings.filterwarnings('ignore', category=DeprecationWarning)
from jsonschema import Draft7Validator, FormatChecker, RefResolver

ROOT=Path(__file__).resolve().parent
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source-root',type=Path,default=ROOT.parents[2],
                    help='actual repository source, also when this contract directory is a scratch copy')
args=parser.parse_args()
checker_path=ROOT/'check_lingdoc_routes.py'
if not checker_path.is_file():
    checker_path=args.source_root/'scripts/ci/check_lingdoc_routes.py'
checker_loader=importlib.util.spec_from_file_location('lingdoc_route_parity',checker_path)
checker=importlib.util.module_from_spec(checker_loader)
checker_loader.loader.exec_module(checker)
# The check vocabulary lives in one module -- scripts/lingdoc_check_kinds.py -- and this
# validator reads it from there.  It used to carry its own shorter copy, which knew four
# kinds while the runner knew ten: a check that used one of the six the validator did not
# know was a static failure, and a check that used a kind nobody knew was invisible.
sys.path.insert(0,str((args.source_root/'scripts').resolve()))
import lingdoc_check_kinds as check_kinds
def read(name): return json.loads((ROOT/name).read_text(encoding='utf-8'))
spec=read('openapi.json')
cases=read('scenarios.json')
flow=read('workflow.json')
route_parity=checker.check(args.source_root,spec)
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
normalize=checker.normalize_openapi_schema
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
operation_methods={}
for path,item in spec['paths'].items():
    for method,op in item.items():
        assert method in {'get','post','put','patch','delete'}
        oid=op['operationId']; assert oid not in ops
        ops[oid]=op
        operation_methods[oid]=method
        assert set(re.findall(r'\{([^}]+)\}',path))=={p['name'] for p in op.get('parameters',[]) if p['in']=='path' and p['required']}
        if 'requestBody' in op:
            media=op['requestBody']['content']['application/json']
            validate(media['schema'],media['example'],oid+' request')
        for code,response in op['responses'].items():
            for media in response.get('content',{}).values():
                for name,example in media.get('examples',{}).items():
                    validate(media['schema'],example['value'],oid+' '+code+' '+name)

scenario_steps=0
scenario_steps_with_requests=0
check_references={'tied':0,'unresolved':0}
VARIABLE=re.compile(r'\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}')
def has_variable(value):
    if isinstance(value,str): return bool(VARIABLE.search(value))
    if isinstance(value,list): return any(has_variable(v) for v in value)
    if isinstance(value,dict): return any(has_variable(v) for v in value.values())
    return False
def variable_names(value):
    if isinstance(value,str): return set(VARIABLE.findall(value))
    if isinstance(value,list): return set().union(*(variable_names(v) for v in value)) if value else set()
    if isinstance(value,dict): return set().union(*(variable_names(v) for v in value.values())) if value else set()
    return set()

def lookup(root,pointer):
    """Read a place in an example, saying plainly when it is not there."""
    current=root
    for part in pointer.lstrip('/').split('/'):
        part=part.replace('~1','/').replace('~0','~')
        if isinstance(current,list):
            if not part.isdigit() or int(part)>=len(current): return False,None
            current=current[int(part)]
        elif isinstance(current,dict):
            if part not in current: return False,None
            current=current[part]
        else: return False,None
    return True,current

def ids_of(collection,id_field):
    """The ids an array carries, whether it holds bare ids or objects."""
    found=set()
    if not isinstance(collection,list): return found
    for item in collection:
        if isinstance(item,str): found.add(item)
        elif isinstance(item,dict) and id_field in item: found.add(item[id_field])
    return found

def tie_expectation(check,kind,example,at,siblings=None):
    """Tie one expectation to the example it is about.

    Returns 'tied' when the example supports the expectation and 'unresolved' when the
    example cannot speak about it -- a captured value, or a place the example does not
    carry.  An example that contradicts the expectation is an error: an assertion that no
    published example supports is a wish, not an expectation.
    """
    path=check['path']
    if path.startswith('{{'): return 'unresolved'
    found,actual=lookup(example,path)
    value=check[kind]
    if kind=='equals':
        if has_variable(value): return 'unresolved'
        assert found,at+': the example does not carry '+path
        assert actual==value,at+': the example holds '+repr(actual)+', not '+repr(value)
        return 'tied'
    if kind=='length':
        assert found,at+': the example does not carry '+path
        assert len(actual)==value,at+': the example has '+str(len(actual))
        return 'tied'
    if kind=='keys':
        assert found,at+': the example does not carry '+path
        assert sorted(actual)==sorted(value),at+': the example has '+str(sorted(actual))
        return 'tied'
    if kind=='one_of':
        assert found,at+': the example does not carry '+path
        assert actual in value,at+': the example holds '+repr(actual)
        return 'tied'
    if kind=='marked_or_absent':
        # The item may be absent: that is one of the two states the assertion allows.
        if not found: return 'tied'
        marker_found,marker=lookup(actual,value)
        assert marker_found,at+': the example does not carry the marker '+value
        assert marker not in (None,'',[],{}),at+': the example marks the item with nothing'
        return 'tied'
    if kind in ('ref_in','not_in'):
        array_found,collection=lookup(example,value)
        assert array_found,at+': the example does not carry '+value
        id_field=check.get('id_field','id')
        assert found,at+': the example does not carry '+path
        present=actual in ids_of(collection,id_field)
        assert present==(kind=='ref_in'),at+': the example does not hold the claimed relation'
        return 'tied'
    if kind=='version_bound':
        targets=value if isinstance(value,list) else [value]
        for target in targets:
            target_found,target_value=lookup(example,target)
            assert found and target_found,at+': the example does not carry the bound version'
            assert actual==target_value,at+': the example does not bind the two versions'
        return 'tied'
    if kind=='readback_equals':
        if has_variable(value): return 'unresolved'
        assert found,at+': the example does not carry '+path
        assert actual==value,at+': the example holds '+repr(actual)+', not '+repr(value)
        return 'tied'
    if kind=='hash_equals':
        source=(siblings or {}).get(value['from_step'])
        assert source is not None,at+': hash_equals names a step this contract does not have'
        source_found,_=lookup(source,path)
        assert source_found,at+': the named step does not carry '+path
        return 'tied'
    if kind=='non_empty':
        assert found,at+': the example does not carry '+path
        assert actual not in (None,'',[],{}),at+': the example holds an empty value'
        return 'tied'
    if kind=='all_non_empty':
        assert found,at+': the example does not carry '+path
        assert isinstance(actual,list) and actual,at+': the example holds an empty collection'
        for entry in actual:
            assert entry not in (None,'',[],{}),at+': the example holds an empty entry'
        return 'tied'
    if kind=='http_error':
        assert found,at+': the example does not carry '+path
        assert isinstance(actual,dict),at+': the example does not carry an error envelope'
        assert actual.get('code')==value.get('code'),at+': the example holds the error code '+repr(actual.get('code'))
        if 'retryable' in value:
            assert actual.get('retryable')==value['retryable'],at+': the example holds another retryable flag'
        return 'tied'
    if kind=='equals_step':
        other=(siblings or {}).get(value['from_step'])
        assert other is not None,at+': equals_step names a step this contract does not have'
        other_found,other_value=lookup(other,value['path'])
        assert other_found,at+': the named step does not carry '+value['path']
        assert found,at+': the example does not carry '+path
        assert actual==other_value,at+': the two steps disagree in the examples'
        return 'tied'
    if kind=='provenance_real':
        # The example may not carry a provenance marker at all: the OpenAPI document
        # does not declare one yet.  Then the example cannot speak about it.
        if not found: return 'unresolved'
        assert actual in check_kinds.REAL_PROVENANCE_VALUES,at+': the example holds the provenance '+repr(actual)
        return 'tied'
    raise AssertionError(at+': no tie is defined for the kind '+kind)

def check_scenario_checks(step,where,examples_by_step=None):
    """Validate the evaluable expectations, and tie every one of them to the published example."""
    checks=step.get('checks')
    if checks is None: return 0,0
    assert isinstance(checks,list) and checks,where+' checks must be a non-empty list'
    example=ptr(ops[step['operation_id']]['responses'][str(step['expected_http'])]['content']['application/json']
                ['examples'][step['response_example']],'/value')
    tied=unresolved=0
    for index,check in enumerate(checks,start=1):
        at=where+' check '+str(index)
        try:
            kind=check_kinds.validate_check(check,at)
        except check_kinds.CheckKindError as error:
            raise AssertionError(str(error)) from None
        expected=check[kind]
        if kind!='equals' and kind!='readback_equals' and has_variable(expected):
            raise AssertionError(at+' cannot tie a '+kind+' expectation to the example')
        verdict=tie_expectation(check,kind,example,at,examples_by_step)
        if verdict=='unresolved': unresolved+=1
        else: tied+=1
    return tied,unresolved

def check_request_shape(op,request,where):
    """The parts of a request a static check can hold without knowing what a variable resolves to.

    A `{{...}}` value has no type here, so field *presence* and field *names* are checked and
    value types are left to the run; claiming more would be pretending to know the environment.
    """
    assert isinstance(request,dict),where+' request must be an object'
    assert set(request)<= {'path_params','query','headers','json'},where+' request has an unknown bucket'
    for bucket in ('path_params','query','headers'):
        assert isinstance(request.get(bucket,{}),dict),where+' '+bucket+' must be an object'
    for parameter in op.get('parameters',[]):
        if parameter['in'] not in {'path','header'}: continue
        bucket=request.get('path_params' if parameter['in']=='path' else 'headers',{})
        if parameter.get('required'):
            present=[name for name in bucket if name.lower()==parameter['name'].lower()]
            assert present,where+' omits required '+parameter['in']+' parameter '+parameter['name']
    declared={p['name'] for p in op.get('parameters',[]) if p['in']=='path'}
    assert set(request.get('path_params',{}))<=declared,where+' sends a path parameter the path does not declare'
    if 'requestBody' in op:
        schema=op['requestBody']['content']['application/json']['schema']
        schema=ptr(spec,schema['$ref']) if '$ref' in schema else schema
        body=request.get('json')
        assert isinstance(body,dict),where+' must send a JSON body for this operation'
        assert set(schema.get('required',[]))<=set(body),where+' omits a required body field'
        if schema.get('additionalProperties') is False:
            unknown=set(body)-set(schema.get('properties',{}))
            assert not unknown,where+' sends body fields the contract does not declare: '+str(sorted(unknown))
    else:
        assert request.get('json') is None,where+' sends a body to an operation that declares none'

def check_flow_checks(step,where,examples_by_step):
    """Validate the F01 contract's expectations, and tie every one of them to its reference response.

    These assertions used to be validated only when the runner reached them: a kind
    nobody knew, or a key no kind declares, stayed invisible until that node ran -- or,
    worse, was silently ignored.  Now the contract is checked before it is run.
    """
    checks=step['checks']
    assert isinstance(checks,list) and checks,where+' checks must be a non-empty list'
    example=step.get('reference_response')
    tied=unresolved=0
    for index,check in enumerate(checks,start=1):
        at=where+' check '+str(index)
        try:
            kind=check_kinds.validate_check(check,at)
        except check_kinds.CheckKindError as error:
            raise AssertionError(str(error)) from None
        if kind!='hash_equals':
            # A hash assertion is about the bytes another step recorded; every other
            # kind is about a place in this step's own response.
            assert example is not None,where+' checks an expectation no reference response stands behind'
        expected=check[kind]
        if kind not in ('equals','readback_equals') and has_variable(expected):
            raise AssertionError(at+' cannot tie a '+kind+' expectation to the example')
        verdict=tie_expectation(check,kind,example,at,examples_by_step)
        if verdict=='unresolved': unresolved+=1
        else: tied+=1
    return tied,unresolved

def check_scenario_step(scenario,index,step):
    global scenario_steps,scenario_steps_with_requests,check_references
    where=step.get('id') or f"{scenario['id']} step {index}"
    assert step['operation_id'] in ops,where+' names an operation the OpenAPI document does not have'
    if 'readback_of' in step:
        assert isinstance(step['readback_of'],str) and step['readback_of'].strip(),where+' readback_of must name an earlier step'
        assert operation_methods[step['operation_id']]=='get',where+' readback step must use GET'
        assert step.get('checks'),where+' readback step must declare at least one check'
    if 'after_step' in step:
        assert isinstance(step['after_step'],str) and step['after_step'].strip(),where+' after_step must name an earlier step'
        assert step.get('checks'),where+' after_step step must declare at least one check'
    response=ops[step['operation_id']]['responses'][str(step['expected_http'])]
    if 'response_example' in step:
        assert step['response_example'] in response['content']['application/json']['examples'],where
    if step.get('actor') is not None:
        assert step['actor'] in ACTORS,where+' names an actor the contract never declares: '+str(step['actor'])
    if 'request' in step:
        # 一个请求能被发出去，除了形状合法还差两样：谁在发（actor），以及重投时凭什么认出
        # 是同一次（幂等键）。两样都得有出处——只查名字写对没写对，删掉它们也照样通过。
        assert step.get('actor'),where+' carries a request but names nobody to send it as'
        required_idempotency = any(
            parameter['in'] == 'header'
            and parameter['name'].lower() == 'idempotency-key'
            and parameter.get('required')
            for parameter in ops[step['operation_id']].get('parameters', [])
        )
        if required_idempotency:
            headers=step['request'].get('headers',{})
            assert any(name.lower()=='idempotency-key' for name in headers),\
                where+' omits the required Idempotency-Key'
        check_request_shape(ops[step['operation_id']],step['request'],where)
        scenario_steps_with_requests+=1
    if 'checks' in step:
        assert 'response_example' in step,where+' checks an expectation no published example stands behind'
        tied,unresolved=check_scenario_checks(step,where)
        check_references['tied']+=tied;check_references['unresolved']+=unresolved
    scenario_steps+=1

# 谁是「调用者」这件事也得有出处：契约里只有所有者（canonical fixture 的立项人）和起点状态
# 声明的协作者两种名字，别的地方冒出来的名字是这个校验器要拦住的那种笔误。
ACTORS={'u-owner'}|{name for state in cases['starting_states'] for name in state['project'].get('members',[])}
def check_same_resource(step,earlier,where):
    """The two steps of a two-step requirement must reach the same resource.

    "Same resource" is machine-checkable here: the two requests name the same
    path coordinates -- the same keys, and the same values (the same captured
    variable, or the same literal).  Without this, a step could declare that it
    asserts the consequence of an operation that touched a different resource
    altogether, and the report would read it as a fact about this one.
    """
    mine=(step.get('request') or {}).get('path_params') or {}
    theirs=(earlier.get('request') or {}).get('path_params') or {}
    assert set(mine)==set(theirs),where+' after_step must name a step on the same resource (path parameters differ: '+str(sorted(mine))+' against '+str(sorted(theirs))+')'
    for name,value in mine.items():
        assert value==theirs[name],where+' after_step must name a step on the same resource (path parameter '+name+' differs: '+repr(value)+' against '+repr(theirs[name])+')'


def check_scenario(scenario):
    observation=scenario.get('white_box_observation')
    if observation is not None:
        assert scenario['id']=='F02','white-box observations are only permitted for F02'
        assert isinstance(observation,dict),'white_box_observation must be an object'
        assert observation.get('id')=='F02-WB-01','F02 white-box observation needs its explicit contract id'
        assert observation.get('classification')=='white_box','F02 observation must be marked white_box'
        observer=observation.get('observer')
        assert isinstance(observer,list) and observer and all(isinstance(part,str) and part.strip() for part in observer),\
            'F02 white-box observation must declare its observer as an argument list'
        counters=observation.get('zero_counters')
        assert isinstance(counters,list) and counters and all(isinstance(item,str) and item.strip() for item in counters),\
            'F02 white-box observation must enumerate its zero counters'
        assert len(counters)==len(set(counters)),'F02 white-box observation repeats a counter'
        assert isinstance(observation.get('boundary'),str) and observation['boundary'].strip(),\
            'F02 white-box observation must state its boundary'
    elif scenario['id']=='F02':
        raise AssertionError('F02 must declare its white-box observation point')
    if scenario.get('starting_state') is not None:
        assert scenario['starting_state'] in {state['id'] for state in cases['starting_states']},scenario['id']
    steps=scenario.get('steps',[])
    for index,step in enumerate(steps,start=1):
        check_scenario_step(scenario,index,step)
        if 'readback_of' in step:
            earlier=next((candidate for candidate in steps[:index-1]
                          if candidate.get('id')==step['readback_of']),None)
            assert earlier is not None,step['id']+' readback_of must name an earlier step'
            assert operation_methods[earlier['operation_id']]!='get',\
                step['id']+' readback_of must name an earlier write step'
        if 'after_step' in step:
            earlier=next((candidate for candidate in steps[:index-1]
                          if candidate.get('id')==step['after_step']),None)
            assert earlier is not None,step['id']+' after_step must name an earlier step'
            check_same_resource(step,earlier,step['id'])
    state=next((item for item in cases['starting_states'] if item['id']==scenario.get('starting_state')),None)
    if state and scenario.get('steps') and all('request' in step for step in scenario['steps']):
        available={'project.id'}
        available|={f"asset.{asset['knowledge_id']}" for asset in state.get('assets') or []}
        available|={f"chapter.{chapter['section_id']}" for chapter in state.get('chapters') or []}
        for index,step in enumerate(scenario['steps'],start=1):
            where=step.get('id') or f"{scenario['id']} step {index}"
            referenced=variable_names(step['request'])|variable_names(step.get('checks',[]))
            missing=referenced-available
            assert not missing,where+' references values not yet captured: '+', '.join(sorted(missing))
            captures=step.get('capture',{})
            assert isinstance(captures,dict),where+' capture must be an object'
            if captures:
                try:
                    example=(ops[step['operation_id']]['responses'][str(step['expected_http'])]['content']
                             ['application/json']['examples'][step['response_example']]['value'])
                except KeyError as error:
                    raise AssertionError(where+' capture needs a published JSON response example') from error
            for name,pointer in captures.items():
                assert isinstance(name,str) and re.fullmatch(r'[A-Za-z_][A-Za-z0-9_.-]*',name),\
                    where+' capture name is invalid'
                assert isinstance(pointer,str) and pointer.startswith('/'),where+' capture pointer must be a JSON pointer'
                assert name not in available,where+' captures an already available variable: '+name
                try: ptr(example,pointer)
                except (KeyError,IndexError,TypeError,ValueError):
                    raise AssertionError(where+' capture pointer is absent from its published response example: '+pointer)
                available.add(name)
for scenario in cases['scenarios']:
    check_scenario(scenario)
# 反例也会走到同一段代码，所以计数在这里就截下来：报告要说的是契约里有几条，不是校验器跑了几遍。
scenario_totals={'steps_with_requests':scenario_steps_with_requests,
                 'checks_tied':check_references['tied'],'checks_unresolved':check_references['unresolved']}

def expand(x,env):
    if isinstance(x,dict): return {k:expand(v,env) for k,v in x.items()}
    if isinstance(x,list): return [expand(v,env) for v in x]
    if not isinstance(x,str): return x
    match=re.fullmatch(r'\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}',x)
    if match: return env[match[1]]
    return re.sub(r'\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}',lambda m:str(env[m[1]]),x)
# A contract may take a value from the environment instead of capturing it
# (the material identifier at F01-06 is one).  This validator checks the
# *shape* of a request, so a placeholder of the right type is enough to
# expand against; the run is where the real value comes from.
RUNTIME_VARIABLES={'knowledge_id':'k-runtime-placeholder'}
env=dict(RUNTIME_VARIABLES);resolved=[]
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

# The F01 contract's own assertions are checked here, not only when the runner reaches
# them.  Every one of them is tied to the reference response of its own step, so an
# assertion the contract cannot back is refused before anybody runs it.
flow_examples={step['id']:step.get('reference_response') for step in flow['steps']}
flow_check_tied=flow_check_unresolved=0
for step in flow['steps']:
    if 'checks' not in step: continue
    tied,unresolved=check_flow_checks(step,step['id'],flow_examples)
    flow_check_tied+=tied;flow_check_unresolved+=unresolved

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
    project_version=None;spec_revision=None;chapters={};candidate=None;seen={};confirmations={};activation_reads={}
    # The model-use consent chain moves three versions at once: the project
    # version, the context revision and the asset revision.  The trace has to
    # carry them, or the contract would claim a consent that is pinned to a
    # revision nobody can name.
    context_revision=None;asset_revision=0;policy_change_sets={}
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
            project_version=1;spec_revision=0;context_revision=data['current_context_revision']
        elif oid=='saveSpec':
            assert body['expected_spec_revision']==spec_revision
            spec_revision+=1;project_version+=1
            assert data['spec_revision']==spec_revision and data['project_version']==project_version
        elif oid=='recordActivationRead':
            assert body['expected_project_version']==project_version
            assert data['actor_id']==step['actor'] and data['project_id']==req['path_params']['projectId']
            assert data['project_version']==project_version and data['spec_revision']==spec_revision
            activation_reads[data['id']]=(data['actor_id'],data['project_id'],data['project_version'],data['spec_revision'])
        elif oid=='activateProject':
            assert activation_reads[body['activation_read_id']]==(step['actor'],req['path_params']['projectId'],project_version,spec_revision)
            assert body['expected_project_version']==project_version and body['reviewed_project_version']==project_version
            assert body['expected_spec_revision']==spec_revision
            project_version+=1
            assert data['project_version']==project_version and data['status']=='active'
            context_revision=data['current_context_revision']
        elif oid=='listChapters': chapters={c['id']:copy.deepcopy(c) for c in data}
        elif oid=='bindAsset':
            # The asset revision is the anchor the consent is pinned to: the
            # proposal must name the revision that is current right now.
            asset_revision=data['asset_revision']
        elif oid=='createChangeDraftPostPolicyProposal':
            assert body['confirmed'] is True,'the consent proposal must be confirmed'
            assert body['expected_version']==asset_revision,'the consent must be pinned to the current asset revision'
            assert body['expected_context_revision']==context_revision,'the consent must name the current context revision'
            assert body['policy']['model_use_allowed'] is True,'the consent this chain exists for is the model-use one'
            assert body['policy']['scope'] in {'personal','project','team'}
            assert body['policy']['purpose'].strip(),'a model-use consent needs a purpose'
            policy_change_sets[data['id']]='draft'
        elif oid=='assessChangeSet':
            assert policy_change_sets.get(req['path_params']['changeSetId'])=='draft','only a draft may be assessed'
            policy_change_sets[req['path_params']['changeSetId']]='assessed'
        elif oid=='applyChangeSet':
            assert policy_change_sets.get(req['path_params']['changeSetId'])=='assessed','only an assessed change set may be applied'
            policy_change_sets[req['path_params']['changeSetId']]='applied'
            project_version+=1;context_revision+=1;asset_revision+=1
        elif oid=='getProjectAssetPolicy':
            assert data['model_use_allowed'] is True,'the consent record must carry the model-use consent'
            assert data['asset_revision']==asset_revision,'the consent must be pinned to the current asset revision'
            assert data['version']==asset_revision
            assert data['context_revision']==context_revision
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
            assert body['expected_chapter_version_id']==ch['current_version_id'] and body['expected_spec_revision']==spec_revision
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
    assert project_version==9 and spec_revision==1

check_trace(resolved)
release_examples=ops['prepareRelease']['responses']['201']['content']['application/json']['examples']
for example in release_examples.values(): check_frozen(example['value']['data'])
assert all(c['chapter_version_id'] is None for c in release_examples['empty_chapters']['value']['data']['frozen_input']['chapters'])
canonical=(ROOT/'frozen-input.canonical.json').read_bytes()
assert canonical==canon(cases['canonical_fixture']['release']['frozen_input'])
assert hashlib.sha256(canonical).hexdigest()==(ROOT/'frozen-input.sha256').read_text().strip()

negative_results=[]
def flow_index(step_id):
    """Resolve stable workflow IDs for all mutation checks, including main's additions."""
    return next(index for index,step in enumerate(resolved) if step.get('id')==step_id)

for label,step_id,mutate in [
 ('reject_lost_review_item','F01-13',lambda s:s['reference_response']['data'].update(review_items=[])),
 ('reject_wrong_expected_version','F01-13',lambda s:s['request']['json'].update(expected_chapter_version_id='wrong')),
 ('reject_frozen_content_changed_without_digest','F01-19',lambda s:s['reference_response']['data']['frozen_input']['spec'].update(research_goal='changed')),
 ('reject_activation_without_observed_receipt','F01-04',lambda s:s['request']['json'].update(activation_read_id='unobserved-receipt')),
 ('reject_activation_receipt_actor_pin','F01-03b',lambda s:s['reference_response']['data'].update(actor_id='other-actor')),
]:
    bad=copy.deepcopy(resolved)
    mutate(next(step for step in bad if step['id']==step_id))
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
badtrace[flow_index('F01-16')]['reference_response']['data']['id']='different-confirmation-returned'
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

# 起点状态的静态形状。真把一份声明造进服务、再读回比对，是 scripts/lingdoc_mock/load_state.py
# 的职责；这里只保证契约里这份声明本身是自洽的，且只声明造得出来的部分。
startable={'project','assets','chapters'}
buildable_states=cases['starting_states']
unique_by(buildable_states,'id')
fixture_sections={s['section_id'] for s in cases['canonical_fixture']['template']['sections']}
# 起点状态可以绑一条「当前不可用」的资料（F03 的前提）。它不是 canonical fixture 那条
# ——canonical 那条是 ready 的——所以这里显式补一个名字，而不是往 canonical 里塞第二份资料。
fixture_knowledge={cases['canonical_fixture']['asset']['knowledge_id'],'k-notready'}
def check_starting_state(state):
    unbuildable=set(state)-startable-{'id','name','note'}
    assert not unbuildable,'starting state declares parts that cannot be built: '+str(sorted(unbuildable))
    assert state.get('id','').strip() and state.get('name','').strip()
    project=state['project']
    assert project['template_id']==cases['canonical_fixture']['template']['id']
    assert project['status'] in {'draft','active'}
    assert project['spec'] and all(isinstance(v,str) and v.strip() for v in project['spec'].values())
    assert set(project['spec'])<=set(ops['getTemplate']['responses']['200']['content']['application/json']['examples']['success']['value']['data']['required_fields'])
    assert all(isinstance(m,str) and m.strip() for m in project.get('members',[]))
    if project['status']=='draft': assert not state.get('chapters'),'template sections only exist once the project is active'
    for asset in state.get('assets',[]):
        assert asset['knowledge_id'] in fixture_knowledge,asset['knowledge_id']
        assert asset['processing_state'] in {'pending','processing','ready','failed','replaced'}
    for chapter in state.get('chapters',[]):
        assert chapter['section_id'] in fixture_sections,chapter['section_id']
        assert isinstance(chapter['body_markdown'],str),'an empty body declares a section with no version yet'
        assert chapter['body_markdown'].strip() or not chapter['source_ids'],'no body cannot cite anything'
        # 标记的字符集以提供方自己的解析为准（internal/lingdoc/workspacecore/service.go 的
        # sourceMarker），不是随便写一个「方括号里什么都行」：静态检查要比运行期至少一样严，
        # 否则契约能过、装载器却拒——那正是这票最不想要的两种结论不一致。
        assert set(re.findall(r'\[\[source:([A-Za-z0-9_-]+)\]\]',chapter['body_markdown']))==set(chapter['source_ids']),'body markers and source_ids disagree'
for state in buildable_states: check_starting_state(state)
for label,mutate in [
 ('starting_state_declares_unbuildable_part',lambda s:s.update(candidate={'id':'candidate-demo'})),
 ('starting_state_unknown_section',lambda s:s['chapters'][0].update(section_id='introduction')),
 ('starting_state_source_marker_without_reference',lambda s:s['chapters'][0].update(body_markdown='带引用 [[source:s-demo]] 的正文')),
 ('starting_state_draft_with_chapters',lambda s:s['project'].update(status='draft')),
 ('starting_state_unknown_spec_field',lambda s:s['project']['spec'].update(invented_condition='x')),
 ('starting_state_unknown_knowledge',lambda s:s.update(assets=[{'knowledge_id':'k-fabricated','processing_state':'ready'}])),
 ('starting_state_unknown_asset_state',lambda s:s.update(assets=[{'knowledge_id':'k-demo','processing_state':'probably'}]))
]:
    bad=copy.deepcopy(next(s for s in buildable_states if s['id']=='S2'));mutate(bad)
    must_reject(label,lambda:check_starting_state(bad))

# 场景步骤的请求、身份与可求值断言。反例都从契约里真实那条步骤改出来，改完必须被拒——
# 不然「静态校验认得这些新键」只是一句话，不是一件事。
F22=next(s for s in cases['scenarios'] if s['id']=='F22')
def scenario_step():
    scenario=next(s for s in cases['scenarios'] if s['id']=='F10')
    return copy.deepcopy(next(step for step in scenario['steps'] if step['operation_id']=='prepareRelease'))
def mutate_step(mutate):
    def run():
        step=scenario_step();mutate(step);check_scenario_step(F22,1,step)
    return run
def mutate_check(mutate):
    def run():
        step=scenario_step();mutate(step['checks'][0]);check_scenario_step(F22,1,step)
    return run
def mutate_flow_check(mutate):
    """Break one of the F01 contract's own assertions and see whether it is refused."""
    def run():
        step=copy.deepcopy(next(s for s in flow['steps'] if s.get('checks')))
        mutate(step['checks'][0])
        check_flow_checks(step,step['id'],flow_examples)
    return run
def replace_flow_kind(check):
    for name in list(check):
        if name in check_kinds.KNOWN_CHECK_OPERATORS:
            del check[name]
    check['not_a_kind_at_all']=64
# 「先操作、再断言」的两步结构：先后、同资源、必须带断言，三条各留一个反例。
# 反例从契约里真实那条两步场景（F24）改出来，改完必须被拒。
F24=next(s for s in cases['scenarios'] if s['id']=='F24')
def f24_step(index):
    return copy.deepcopy(F24['steps'][index])
F05_WRITE=copy.deepcopy(next(step for step in next(s for s in cases['scenarios'] if s['id']=='F05')['steps']
                             if step['operation_id']=='saveChapter'))
for label,action in [
    ('workflow_check_with_an_unknown_kind',mutate_flow_check(replace_flow_kind)),
    ('workflow_check_with_an_unknown_key_next_to_a_known_kind',
     mutate_flow_check(lambda c:c.update(also_not_a_kind='x'))),
    ('workflow_check_whose_expectation_the_reference_response_does_not_support',
     mutate_flow_check(lambda c:c.update(equals='other_code'))),
    ('scenario_check_with_two_operators',mutate_check(lambda c:c.update(one_of=['x']))),
    ('scenario_check_disagrees_with_the_published_example',mutate_check(lambda c:c.update(equals='other_code'))),
    ('scenario_check_reads_a_place_the_example_does_not_have',mutate_check(lambda c:c.update(path='/error/nope'))),
    ('scenario_check_without_an_intent',mutate_check(lambda c:c.pop('intent'))),
    ('scenario_check_without_an_operator',mutate_check(lambda c:c.pop('equals'))),
    ('scenario_check_with_an_unknown_key',mutate_check(lambda c:c.update(assertion='prose'))),
    ('scenario_check_on_a_step_with_no_published_example',mutate_step(lambda s:s.pop('response_example'))),
    ('scenario_request_omits_a_required_path_parameter',mutate_step(lambda s:s['request']['path_params'].pop('projectId'))),
    ('scenario_request_sends_a_field_the_contract_does_not_declare',
     mutate_step(lambda s:s['request']['json'].update(invented_field='x'))),
    ('scenario_request_omits_a_required_body_field',mutate_step(lambda s:s['request']['json'].pop('asset_ids'))),
    ('scenario_request_sends_a_body_the_operation_does_not_take',mutate_step(lambda s:s.update(
        operation_id='listProjects',request={'path_params':{},'headers':{},'json':{'query':'x'}}))),
    ('scenario_request_without_an_actor',mutate_step(lambda s:s.pop('actor'))),
    ('scenario_request_without_an_idempotency_key',
     mutate_step(lambda s:s['request']['headers'].pop('Idempotency-Key'))),
    ('scenario_step_names_an_undeclared_actor',mutate_step(lambda s:s.update(actor='u-stranger'))),
    ('scenario_step_points_at_an_unknown_operation',mutate_step(lambda s:s.update(operation_id='inventOperation'))),
    ('scenario_readback_must_use_a_read_operation',
     mutate_step(lambda s:s.update(readback_of='F22-00'))),
    ('scenario_readback_needs_a_check',
     lambda:check_scenario_step(next(s for s in cases['scenarios'] if s['id']=='F05'),1,
                                {**copy.deepcopy(next(s for s in cases['scenarios'] if s['id']=='F05')['steps'][0]),
                                 'readback_of':'F05-02','checks':[]})),
    ('scenario_readback_must_point_to_an_earlier_step',
     lambda:check_scenario({**copy.deepcopy(next(s for s in cases['scenarios'] if s['id']=='F05')),
                            'steps':[dict(copy.deepcopy(next(s for s in cases['scenarios'] if s['id']=='F05')['steps'][0]),
                                          readback_of='F05-01')]})),
    ('scenario_declares_an_unknown_starting_state',
     lambda:check_scenario({**F22,'starting_state':'S99'})),
    ('white_box_observation_is_restricted_to_f02',
     lambda:check_scenario({**F22,'white_box_observation':copy.deepcopy(next(s for s in cases['scenarios']
                                                                           if s['id']=='F02')['white_box_observation'])})),
    ('scenario_uses_a_capture_before_it_is_produced',
     lambda:check_scenario({**copy.deepcopy(next(s for s in cases['scenarios'] if s['id']=='F08')),
                            'steps':[dict(next(s for s in cases['scenarios'] if s['id']=='F08')['steps'][0],
                                          request={'path_params':{'projectId':'{{f08.run_id}}'},'headers':{},
                                                   'json':next(s for s in cases['scenarios'] if s['id']=='F08')['steps'][0]['request']['json']})]})),
    ('scenario_capture_points_outside_the_published_response',
     lambda:check_scenario({**copy.deepcopy(next(s for s in cases['scenarios'] if s['id']=='F08')),
                            'steps':[dict(next(s for s in cases['scenarios'] if s['id']=='F08')['steps'][0],
                                          capture={'f08.missing':'/data/not_a_field'})]})),
    ('scenario_after_step_needs_a_check',
     lambda:check_scenario_step(F24,3,{**f24_step(2),'checks':[]})),
    ('scenario_after_step_must_name_an_earlier_step',
     lambda:check_scenario({**copy.deepcopy(F24),'steps':[f24_step(0),f24_step(2)]})),
    ('scenario_after_step_must_reach_the_same_resource',
     lambda:check_scenario({**copy.deepcopy(F24),'steps':[
         {**f24_step(1),'id':'F24-90'},
         {**f24_step(2),'after_step':'F24-90',
          'request':{**f24_step(2)['request'],
                     'path_params':{'projectId':'{f24.other_project}'}}}]})),
    ('scenario_after_step_must_reach_the_same_coordinates',
     lambda:check_scenario({**copy.deepcopy(F24),'steps':[
         {**F05_WRITE,'id':'F24-90'},
         {**f24_step(2),'after_step':'F24-90'}]})),
]:
    must_reject(label,action)
assert scenario_totals['steps_with_requests']>=1,'no scenario step declares a request, so nothing above was exercised'
assert scenario_totals['checks_tied']>=1,'no check was tied back to a published response example'

# 采纳确认载荷有两份副本：OpenAPI 的 AdoptionConfirmation 与 G7 契约档里内联的
# 确认块。两份各写各的就会漂移，而漂移的后果是客户端照着契约写出一个服务端根本
# 不认的字段名——契约说 target、服务端只认 adoption_target，这道请求会被静默丢字段。
# 这里逐字比对字段名、必填集与取值集，并留一个反例证明这条比对真的拦得住。
G7_CONTRACT=ROOT/'g7-writing-contract.json'
assert G7_CONTRACT.is_file(),'g7-writing-contract.json is missing from the contract directory'
g7=json.loads(G7_CONTRACT.read_text(encoding='utf-8'))
openapi_confirmation=spec['components']['schemas']['AdoptionConfirmation']
g7_confirmation=g7['schemas']['ApplySelectedRewriteRequest']['properties']['confirmation']
def check_confirmation_shape(openapi_schema,contract_schema):
    assert set(contract_schema['properties'])==set(openapi_schema['properties']),\
        'the confirmation payload declares different fields: '+str(sorted(set(contract_schema['properties'])^set(openapi_schema['properties'])))
    assert set(contract_schema['required'])==set(openapi_schema['required']),\
        'the confirmation payload requires different fields: '+str(sorted(set(contract_schema['required'])^set(openapi_schema['required'])))
    for name,schema in openapi_schema['properties'].items():
        if 'enum' not in schema: continue
        assert contract_schema['properties'][name].get('enum')==schema['enum'],\
            'the confirmation payload disagrees on the accepted values of '+name
check_confirmation_shape(openapi_confirmation,g7_confirmation)
must_reject('g7_contract_confirmation_declares_a_different_field',
            lambda:check_confirmation_shape(openapi_confirmation,
                {**g7_confirmation,
                 'properties':{**g7_confirmation['properties'],
                               'target':g7_confirmation['properties']['adoption_target']}}))
must_reject('g7_contract_confirmation_requires_a_different_field',
            lambda:check_confirmation_shape(openapi_confirmation,
                {**g7_confirmation,'required':[name for name in g7_confirmation['required'] if name!='adoption_target']}))
must_reject('g7_contract_confirmation_widens_the_accepted_values',
            lambda:check_confirmation_shape(openapi_confirmation,
                {**g7_confirmation,
                 'properties':{**g7_confirmation['properties'],
                               'origin':{**g7_confirmation['properties']['origin'],
                                         'enum':g7_confirmation['properties']['origin']['enum']+['']}}}))

report={'result':'PASS','scope':'static_design_artifacts_and_reference_trace_only','route_parity':route_parity,'paths':len(spec['paths']),
 'operations':len(ops),'core_operations':sum(o['x-delivery-phase']=='core' for o in ops.values()),
 'schemas':len(spec['components']['schemas']),'local_refs':refs,'shape_checks':checks,
 'scenarios':len(cases['scenarios']),'scenario_example_references':scenario_steps,'continuous_trace_steps':len(resolved),
 'scenario_steps_with_requests':scenario_totals['steps_with_requests'],
 'scenario_checks_tied_to_a_published_example':scenario_totals['checks_tied'],
 'scenario_checks_left_to_the_run':scenario_totals['checks_unresolved'],
 'continuous_trace_checks_tied_to_a_reference_response':flow_check_tied,
 'continuous_trace_checks_left_to_the_run':flow_check_unresolved,
 'check_kinds':list(check_kinds.KNOWN_CHECK_OPERATORS),
 'starting_states':len(buildable_states),'starting_state_parts_buildable':sorted(startable),
 'negative_static_cases_rejected':negative_results,
 'not_run':['real provider/database/model tests','actual DOCX export/opening','running a scenario step and evaluating its checks against a real response (scripts/lingdoc_mock/run_scenario.py does that against a real service; this static check ties the expectations to the published examples and stops there)','building a declared starting state into a provider and reading it back (scripts/lingdoc_mock/load_state.py does that against a real service; this static check does not)'],
 'external_validation':[{'item':'complete OpenAPI meta-schema validation','tool':'jsonschema 4.26.0 (Draft4Validator) against https://spec.openapis.org/oas/3.0/schema/2021-09-28','result':'PASS (0 errors)','head':'7341aa8','date':'2026-10-19','note':'plus openapi-spec-validator 0.9.0, also PASS'},{'item':'Prism runtime','tool':'@stoplight/prism-cli 5.16.0 in proxy mode against the live backend','result':'PASS (the impact-assessment response raised 0 violations; a negative control with a deliberately broken body raised 4)','head':'7341aa8','date':'2026-10-19'}],'note':'This validates a proposed trace, not implemented server behavior. Export hash remains an explicit placeholder in examples.'}
(ROOT/'validation-result.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(report,ensure_ascii=False))
