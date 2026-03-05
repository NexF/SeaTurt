#!/usr/bin/env python3
"""
SeaTurt 多模态测试脚本
用法:
  1. 纯文本:       python3 test_multimodal.py --text "你好"
  2. 图片+文字:     python3 test_multimodal.py --text "描述这张图片" --image path/to/image.png
  3. 纯图片上传:    python3 test_multimodal.py --image path/to/image.png
  4. multipart上传: python3 test_multimodal.py --text "描述图片" --image path/to/image.png --multipart
  5. 查看历史:      python3 test_multimodal.py --history
  6. 清除历史:      python3 test_multimodal.py --clear-history

需要先创建并启动 agent，或用 --agent-id 指定已有 agent。
"""

import argparse
import base64
import json
import mimetypes
import sys
from pathlib import Path

try:
    import requests
except ImportError:
    print("需要 requests 库: pip3 install requests")
    sys.exit(1)

DEFAULT_BASE_URL = "http://localhost:8080"


def get_or_create_agent(base_url: str, agent_id: str | None) -> str:
    """获取或创建一个 agent，返回 agent_id。"""
    if agent_id:
        resp = requests.get(f"{base_url}/api/agents/{agent_id}")
        if resp.status_code == 200:
            ag = resp.json()
            print(f"[agent] 使用已有 agent: {ag['id']} ({ag['name']}) status={ag['status']}")
            return ag["id"]
        else:
            print(f"[agent] agent {agent_id} 不存在，将创建新的")

    # 查看是否已有 agent
    resp = requests.get(f"{base_url}/api/agents")
    if resp.status_code != 200:
        print(f"[error] 获取 agent 列表失败: HTTP {resp.status_code}: {resp.text}")
        sys.exit(1)
    agents = resp.json()
    if agents:
        ag = agents[0]
        print(f"[agent] 使用第一个 agent: {ag['id']} ({ag['name']}) status={ag['status']}")
        return ag["id"]

    # 创建新的
    resp = requests.post(f"{base_url}/api/agents", json={"name": "multimodal-test"})
    if resp.status_code not in (200, 201):
        print(f"[error] 创建 agent 失败: HTTP {resp.status_code}: {resp.text}")
        sys.exit(1)
    ag = resp.json()
    print(f"[agent] 创建新 agent: {ag['id']} ({ag['name']})")

    # 启动
    resp = requests.post(f"{base_url}/api/agents/{ag['id']}/start")
    if resp.status_code != 200:
        print(f"[warn] 启动 agent 失败: HTTP {resp.status_code}: {resp.text}")
        print(f"[warn] 继续使用，但 chat 可能会失败")
    else:
        print(f"[agent] agent 已启动")

    return ag["id"]


def read_image(image_path: str) -> tuple[str, str]:
    """读取图片文件，返回 (base64_data, mime_type)。"""
    p = Path(image_path)
    if not p.exists():
        print(f"错误: 图片文件不存在: {image_path}")
        sys.exit(1)

    mime_type = mimetypes.guess_type(str(p))[0] or "image/png"
    raw = p.read_bytes()
    b64 = base64.standard_b64encode(raw).decode()

    size_kb = len(raw) / 1024
    print(f"[image] 文件: {p.name}, 大小: {size_kb:.1f} KB, MIME: {mime_type}")
    return b64, mime_type


def chat_json(base_url: str, agent_id: str, text: str | None, image_path: str | None):
    """使用 JSON 模式发送消息（支持多模态）。"""
    content = []

    if text:
        content.append({"type": "text", "text": text})

    if image_path:
        b64, mime = read_image(image_path)
        content.append({
            "type": "image",
            "image": {
                "data": b64,
                "mime_type": mime,
            }
        })

    if not content:
        print("错误: 需要 --text 或 --image")
        sys.exit(1)

    print(f"\n[chat] 发送 JSON 请求 ({len(content)} 个 content block)...")
    print(f"  blocks: {[b['type'] for b in content]}")

    url = f"{base_url}/api/agents/{agent_id}/chat"
    resp = requests.post(url, json={"content": content}, stream=True)

    if resp.status_code != 200:
        print(f"[error] HTTP {resp.status_code}: {resp.text}")
        return

    print(f"\n[stream] 接收 SSE 响应:\n{'='*50}")
    print_sse_stream(resp)


def chat_multipart(base_url: str, agent_id: str, text: str | None, image_path: str):
    """使用 multipart/form-data 模式上传图片。"""
    p = Path(image_path)
    if not p.exists():
        print(f"错误: 图片文件不存在: {image_path}")
        sys.exit(1)

    mime_type = mimetypes.guess_type(str(p))[0] or "image/png"
    size_kb = p.stat().st_size / 1024
    print(f"[image] 文件: {p.name}, 大小: {size_kb:.1f} KB, MIME: {mime_type}")

    files = {"image": (p.name, p.open("rb"), mime_type)}
    data = {}
    if text:
        data["text"] = text

    print(f"\n[chat] 发送 multipart 请求...")

    url = f"{base_url}/api/agents/{agent_id}/chat"
    resp = requests.post(url, data=data, files=files, stream=True)

    if resp.status_code != 200:
        print(f"[error] HTTP {resp.status_code}: {resp.text}")
        return

    print(f"\n[stream] 接收 SSE 响应:\n{'='*50}")
    print_sse_stream(resp)


def print_sse_stream(resp):
    """解析并打印 SSE stream。"""
    full_text = ""
    resp.encoding = "utf-8"
    for line in resp.iter_lines(decode_unicode=True):
        if not line:
            continue
        if not line.startswith("data: "):
            continue
        data_str = line[6:]
        try:
            event = json.loads(data_str)
        except json.JSONDecodeError:
            print(f"  [raw] {data_str}")
            continue

        etype = event.get("type", "unknown")

        if etype == "text_delta":
            text = event.get("data", {}).get("content", "")
            print(text, end="", flush=True)
            full_text += text
        elif etype == "tool_call":
            tc = event.get("data", {})
            print(f"\n  [tool_call] {tc.get('name', '?')}({json.dumps(tc.get('arguments', {}), ensure_ascii=False)[:100]})")
        elif etype == "tool_result":
            tr = event.get("data", {})
            content_preview = json.dumps(tr.get("content", ""), ensure_ascii=False)[:200]
            print(f"  [tool_result] {content_preview}")
        elif etype == "error":
            msg = event.get("data", {}).get("message", "unknown")
            print(f"\n  [ERROR] {msg}")
        elif etype == "done":
            pass
        else:
            print(f"\n  [{etype}] {json.dumps(event.get('data', {}), ensure_ascii=False)[:150]}")

    print(f"\n{'='*50}")
    if full_text:
        print(f"[完整回复] {len(full_text)} 字符")


def show_history(base_url: str, agent_id: str):
    """查看对话历史。"""
    resp = requests.get(f"{base_url}/api/agents/{agent_id}/history")
    resp.raise_for_status()
    messages = resp.json()

    print(f"\n[history] 共 {len(messages)} 条消息:\n{'='*50}")
    for msg in messages:
        role = msg["role"]
        content = msg.get("content", [])
        ts = msg.get("created_at", "")

        blocks_summary = []
        for b in content:
            if b["type"] == "text":
                text = b.get("text", "")
                blocks_summary.append(f'text("{text[:80]}{"..." if len(text) > 80 else ""}")')
            elif b["type"] == "image":
                img = b.get("image", {})
                has_data = bool(img.get("data"))
                has_file = bool(img.get("file_path"))
                mime = img.get("mime_type", "?")
                if has_data:
                    size_kb = len(img["data"]) * 3 / 4 / 1024
                    blocks_summary.append(f"image({mime}, ~{size_kb:.0f}KB)")
                elif has_file:
                    blocks_summary.append(f"image({mime}, file={img['file_path']})")
                else:
                    blocks_summary.append(f"image({mime}, empty)")
            else:
                blocks_summary.append(f"{b['type']}(...)")

        print(f"  [{role}] {' + '.join(blocks_summary)}")
        if ts:
            print(f"         at {ts}")

    print(f"{'='*50}")


def clear_history(base_url: str, agent_id: str):
    """清除对话历史。"""
    resp = requests.delete(f"{base_url}/api/agents/{agent_id}/history")
    resp.raise_for_status()
    print(f"[history] 已清除 agent {agent_id} 的对话历史")


def main():
    parser = argparse.ArgumentParser(description="SeaTurt 多模态测试脚本")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="服务器地址 (默认 http://localhost:8080)")
    parser.add_argument("--agent-id", help="指定 agent ID (不指定则自动选择/创建)")
    parser.add_argument("--text", "-t", help="文本消息内容")
    parser.add_argument("--image", "-i", help="图片文件路径")
    parser.add_argument("--multipart", "-m", action="store_true", help="使用 multipart/form-data 上传 (默认用 JSON)")
    parser.add_argument("--history", action="store_true", help="查看对话历史")
    parser.add_argument("--clear-history", action="store_true", help="清除对话历史")
    args = parser.parse_args()

    base_url = args.base_url.rstrip("/")

    # 健康检查
    try:
        resp = requests.get(f"{base_url}/health", timeout=3)
        resp.raise_for_status()
        print(f"[server] {base_url} 连接正常")
    except Exception as e:
        print(f"[error] 无法连接到服务器 {base_url}: {e}")
        sys.exit(1)

    if not args.text and not args.image and not args.history and not args.clear_history:
        print("请指定 --text 或 --image，或使用 --history 查看历史\n")
        parser.print_help()
        sys.exit(1)

    agent_id = get_or_create_agent(base_url, args.agent_id)

    if args.clear_history:
        clear_history(base_url, agent_id)
        return

    if args.history:
        show_history(base_url, agent_id)
        return

    if args.multipart:
        if not args.image:
            print("--multipart 模式需要 --image")
            sys.exit(1)
        chat_multipart(base_url, agent_id, args.text, args.image)
    else:
        chat_json(base_url, agent_id, args.text, args.image)


if __name__ == "__main__":
    main()
