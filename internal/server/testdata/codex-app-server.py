#!/usr/bin/env python3
"""Deterministic app-server fixture: no network, credentials or real agents."""
import json, pathlib, sys
history_path = pathlib.Path.cwd() / 'codex-fixture-history.json'
history = json.loads(history_path.read_text()) if history_path.exists() else []
initialized = False
active = None

def send(value):
    print(json.dumps(value), flush=True)

def event(method, params):
    send({'method': method, 'params': params})

def finish(status='completed'):
    global active
    if active is None:
        return
    item = {'id': active + '-answer', 'type': 'agentMessage', 'text': 'Hello from **Codex**.'}
    event('item/started', {'item': {**item, 'text': ''}})
    event('item/agentMessage/delta', {'itemId': item['id'], 'delta': item['text']})
    event('item/completed', {'item': item})
    history[-1]['items'].append(item)
    history_path.write_text(json.dumps(history))
    event('thread/tokenUsage/updated', {'tokenUsage': {'total': {'inputTokens':len(history)*10,'cachedInputTokens':len(history)*2,'outputTokens':len(history)*5,'totalTokens':len(history)*15}}})
    event('turn/completed', {'turn': {'id': active, 'status': status}})
    active = None

for line in sys.stdin:
    request = json.loads(line)
    method = request.get('method')
    params = request.get('params') or {}
    identity = request.get('id')
    if method is None:
        event('serverRequest/resolved', {'requestId': identity})
        finish()
        continue
    if method == 'initialized':
        initialized = True
        continue
    if method == 'initialize':
        send({'id': identity, 'result': {'userAgent': 'fixture'}})
        continue
    if not initialized:
        send({'id': identity, 'error': {'message': 'Not initialized'}})
        continue
    if method in ('thread/start', 'thread/resume'):
        if method == 'thread/start':history = []
        if method == 'thread/resume' and params['threadId'] != 'fixture-thread':
            send({'id': identity, 'error': {'message': 'Saved conversation not found'}})
            continue
        send({'id': identity, 'result': {'thread': {'id': 'fixture-thread', 'turns': history}}})
    elif method == 'turn/start':
        prompt = params['input'][0]['text']
        if prompt == 'reject':
            send({'id': identity, 'error': {'message': 'Please select another model'}})
            continue
        active = 'turn-' + str(len(history))
        user = {'id': active+'-user', 'type': 'userMessage', 'content': params['input']}
        history.append({'id': active, 'items': [user]})
        event('turn/started', {'turn': {'id': active}})
        event('item/completed', {'item': user})
        send({'id': identity, 'result': {'turn': {'id': active}}})
        if prompt == 'approval':
            event('item/started', {'item': {'id': 'command', 'type': 'commandExecution', 'command': 'echo approved', 'status': 'inProgress'}})
            send({'id': 900, 'method': 'item/commandExecution/requestApproval', 'params': {'itemId':'command', 'command':'echo approved', 'reason':'Fixture approval'}})
        elif prompt == 'question':
            send({'id': 901, 'method': 'item/tool/requestUserInput', 'params': {'questions': [{'id':'color','header':'Color','question':'Which color?', 'options':[{'label':'Green','description':'Keep the theme'},{'label':'Blue','description':'Change it'}]}]}})
        elif prompt != 'hold':
            finish()
    elif method == 'turn/interrupt':
        finish('interrupted')
        send({'id': identity, 'result': {}})
    elif method == 'model/list':
        send({'id': identity, 'result': {'data': [{'model':'fixture-model','displayName':'Fixture model','supportedReasoningEfforts':[{'reasoningEffort':'medium','description':'Balanced'}]}], 'nextCursor':None}})
