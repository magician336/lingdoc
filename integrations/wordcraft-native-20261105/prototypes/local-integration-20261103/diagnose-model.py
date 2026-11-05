"""Bounded local diagnostic. Credentials stay in memory; print metadata only."""
import json
import sys
import time
import urllib.request
import urllib.error
from pathlib import Path

root = Path(__file__).resolve().parents[2]
config = json.loads((root / 'candidates/lingdoc-native-workspace-20261027/runtime/ai-private/local-ai.json').read_text())
started = time.monotonic()
request = urllib.request.Request('https://api.deepseek.com/v1/models', headers={'Authorization': 'Bearer ' + config['api_key']})
result = {'operation': 'models', 'configured_model': config['model']}
if '--chat' in sys.argv:
    result['operation'] = 'minimal-chat'
    payload = {'model': config['model'], 'max_tokens': 2048, 'temperature': 0.2, 'thinking': {'type': 'disabled'}, 'response_format': {'type': 'json_object'}, 'messages': [{'role': 'user', 'content': 'Return JSON only: {"reply":"ok","citations":[]}'}]}
    request = urllib.request.Request('https://api.deepseek.com/v1/chat/completions', data=json.dumps(payload).encode(), headers={'Authorization': 'Bearer ' + config['api_key'], 'Content-Type': 'application/json'})
try:
    with urllib.request.urlopen(request, timeout=60) as response:
        payload = json.load(response)
        result.update(http_status=response.status)
        if result['operation'] == 'models':
            result['configured_model_exists'] = any(item.get('id') == config['model'] for item in payload.get('data', []))
        else:
            result['has_completion'] = bool(payload.get('choices'))
            result['finish_reason'] = payload.get('choices', [{}])[0].get('finish_reason')
except urllib.error.HTTPError as error:
    result['http_status'] = error.code
    try:
        body = json.load(error).get('error', {})
        for key in ('code', 'type'):
            value = body.get(key)
            if isinstance(value, str) and len(value) < 100 and all(c.isalnum() or c in '_-.' for c in value):
                result['error_' + key] = value
    except Exception:
        pass
except Exception as error:
    result['error_type'] = type(error).__name__
result['elapsed_seconds'] = round(time.monotonic() - started, 3)
print(json.dumps(result))
