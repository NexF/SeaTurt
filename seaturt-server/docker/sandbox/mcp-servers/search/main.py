#!/usr/bin/env python3
"""MCP Server for web search and fetch (stdio transport, JSON-RPC 2.0)."""

import json
import sys

from search import web_search
from fetch import web_fetch

# --- Tool definitions (MCP tools/list) ---

TOOLS = [
    {
        "name": "web_search",
        "description": (
            "Search the web for real-time information. Returns relevant results "
            "with titles, URLs, and content snippets. Use this when you need "
            "current information, facts, documentation, or anything not in your "
            "training data."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Search query (natural language or keywords)",
                },
                "max_results": {
                    "type": "integer",
                    "description": "Maximum number of results to return (default: 5, max: 10)",
                },
                "search_depth": {
                    "type": "string",
                    "enum": ["basic", "advanced"],
                    "description": (
                        "Search depth: 'basic' for quick results, 'advanced' "
                        "for deeper extraction with full content (default: basic)"
                    ),
                },
            },
            "required": ["query"],
        },
    },
    {
        "name": "web_fetch",
        "description": (
            "Fetch and extract readable content from a URL. Returns the page "
            "title and main text content as Markdown. Use this to read "
            "documentation, articles, or any web page."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "URL to fetch",
                },
                "max_length": {
                    "type": "integer",
                    "description": (
                        "Maximum content length in characters "
                        "(default: 8000, max: 32000). Truncates if exceeded."
                    ),
                },
            },
            "required": ["url"],
        },
    },
]


def write_response(obj: dict) -> None:
    """Write a JSON-RPC response to stdout (one line)."""
    line = json.dumps(obj, ensure_ascii=False)
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def write_result(req_id, result) -> None:
    write_response({"jsonrpc": "2.0", "id": req_id, "result": result})


def write_error(req_id, code: int, message: str) -> None:
    write_response({
        "jsonrpc": "2.0",
        "id": req_id,
        "error": {"code": code, "message": message},
    })


def handle_initialize(req_id) -> None:
    write_result(req_id, {
        "protocolVersion": "2024-11-05",
        "capabilities": {"tools": {}},
        "serverInfo": {"name": "mcp-server-search", "version": "1.0.0"},
    })


def handle_tools_list(req_id) -> None:
    write_result(req_id, {"tools": TOOLS})


def handle_tools_call(req_id, params: dict) -> None:
    name = params.get("name", "")
    args = params.get("arguments", {})

    try:
        if name == "web_search":
            query = args.get("query", "")
            if not query:
                write_error(req_id, -32602, "missing required argument: query")
                return
            max_results = int(args.get("max_results", 5))
            search_depth = args.get("search_depth", "basic")
            text = web_search(query, max_results=max_results, search_depth=search_depth)

        elif name == "web_fetch":
            url = args.get("url", "")
            if not url:
                write_error(req_id, -32602, "missing required argument: url")
                return
            max_length = int(args.get("max_length", 8000))
            text = web_fetch(url, max_length=max_length)

        else:
            write_error(req_id, -32602, f"unknown tool: {name}")
            return

        write_result(req_id, {
            "content": [{"type": "text", "text": text}],
            "isError": False,
        })

    except Exception as e:
        write_result(req_id, {
            "content": [{"type": "text", "text": f"Error: {e}"}],
            "isError": True,
        })


def main() -> None:
    """Main loop: read JSON-RPC requests from stdin, write responses to stdout."""
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue

        try:
            req = json.loads(line)
        except json.JSONDecodeError:
            write_error(None, -32700, "parse error")
            continue

        req_id = req.get("id")
        method = req.get("method", "")

        if method == "initialize":
            handle_initialize(req_id)
        elif method == "notifications/initialized":
            # Client notification, no response needed
            pass
        elif method == "tools/list":
            handle_tools_list(req_id)
        elif method == "tools/call":
            handle_tools_call(req_id, req.get("params", {}))
        else:
            write_error(req_id, -32601, f"method not found: {method}")


if __name__ == "__main__":
    main()
