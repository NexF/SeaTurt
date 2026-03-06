import re, json, sys
from collections import Counter

with open('server.log') as f:
    content = f.read()

# Find the failed request (body_bytes=6422) and the one before it
# Let's just look at the raw request info lines
lines = content.split('\n')

print("=== All LLM request summary lines ===")
for i, line in enumerate(lines):
    if 'msg="llm request"' in line and 'body_bytes' in line:
        # Extract key info
        m_msgs = re.search(r'messages=(\d+)', line)
        m_tools = re.search(r'tools=(\d+)', line)
        m_bytes = re.search(r'body_bytes=(\d+)', line)
        m_time = re.search(r'time=([^\s]+)', line)
        msgs = m_msgs.group(1) if m_msgs else '?'
        tools = m_tools.group(1) if m_tools else '?'
        bbytes = m_bytes.group(1) if m_bytes else '?'
        ts = m_time.group(1) if m_time else '?'
        print(f'  line={i} time={ts} messages={msgs} tools={tools} body_bytes={bbytes}')

# Now let's extract the LAST two request bodies using a different approach
# slog wraps with \" and \\n - we need to handle the double escaping
bodies_raw = []
for line in lines:
    m = re.search(r'msg="llm request body" body="(.+)"$', line)
    if m:
        bodies_raw.append(m.group(1))

print(f'\n=== Found {len(bodies_raw)} request bodies ===')

# For the last 2 bodies, just extract the messages structure directly from raw
for idx in range(max(0, len(bodies_raw)-2), len(bodies_raw)):
    raw = bodies_raw[idx]
    print(f'\n--- Body {idx+1} (raw_len={len(raw)}) ---')
    
    # Count messages by looking for "role" patterns
    roles = re.findall(r'\\?"role\\?":\s*\\?"(\w+)\\?"', raw)
    print(f'  roles found: {roles}')
    
    # Look for tool_call_id
    tcids = re.findall(r'\\?"tool_call_id\\?":\s*\\?"([^"\\]+)', raw)
    print(f'  tool_call_ids: {tcids}')
    
    # Look for tool_calls
    tc_names = re.findall(r'\\?"name\\?":\s*\\?"([\w_]+)\\?"', raw)
    print(f'  all names (tools+functions): {tc_names}')
    
    # Count tools in the tools array - look for function definitions
    # The tools array comes after messages
    tool_defs = re.findall(r'"type\\?":\s*\\?"function\\?".*?"name\\?":\s*\\?"([\w_]+)\\?"', raw)
    print(f'  tool definitions: {tool_defs}')
    
    # Check for duplicate tools
    if len(tool_defs) != len(set(tool_defs)):
        dupes = {k:v for k,v in Counter(tool_defs).items() if v > 1}
        print(f'  *** DUPLICATE TOOLS: {dupes} ***')
    
    # Look for image/base64 content
    if 'image' in raw.lower() or 'base64' in raw.lower():
        print(f'  *** CONTAINS IMAGE DATA ***')
    
    # Extract content of tool message
    # Look for tool role message content
    tool_msgs = re.findall(r'"role\\?":\s*\\?"tool\\?".*?"content\\?":\s*\\?"([^"]{0,200})', raw)
    print(f'  tool message contents (first 200 chars each): {tool_msgs}')
