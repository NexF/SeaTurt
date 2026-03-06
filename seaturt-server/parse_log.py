import re, json, sys
from collections import Counter

with open('server.log') as f:
    content = f.read()

bodies = []
for line in content.split('\n'):
    m = re.search(r'msg="llm request body" body="(.+)"$', line)
    if m:
        bodies.append(m.group(1))

print(f'Total request bodies found: {len(bodies)}')
for i, b in enumerate(bodies[-4:]):
    print(f'\n=== Body {len(bodies)-4+i+1} (raw_len={len(b)}) ===')
    try:
        unescaped = b.replace('\\"', '"').replace('\\n', '\n')
        obj = json.loads(unescaped)
        msgs = obj.get('messages', [])
        tools = obj.get('tools', [])
        print(f'messages={len(msgs)} tools={len(tools)}')
        for j, msg in enumerate(msgs):
            role = msg.get('role')
            tc = msg.get('tool_calls')
            tcid = msg.get('tool_call_id')
            cv = msg.get('content', '')
            ct = type(cv).__name__
            cp = str(cv)[:200] if cv else '(empty/null)'
            extra = ''
            if tc:
                extra += f' tool_calls={json.dumps(tc, ensure_ascii=False)[:300]}'
            if tcid:
                extra += f' tool_call_id={tcid}'
            print(f'  [{j}] role={role} content_type={ct} content={cp}{extra}')
        tool_names = [t['function']['name'] for t in tools]
        print(f'  tools: {tool_names}')
        if len(tool_names) != len(set(tool_names)):
            dupes = {k:v for k,v in Counter(tool_names).items() if v > 1}
            print(f'  *** DUPLICATE TOOLS: {dupes} ***')
    except Exception as e:
        print(f'  Parse error: {e}')
        print(f'  Raw (first 500): {b[:500]}')
