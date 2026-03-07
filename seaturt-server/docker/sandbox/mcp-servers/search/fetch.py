"""web_fetch tool implementation using httpx + html2text."""

import httpx
import html2text

_USER_AGENT = (
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)


def web_fetch(url: str, max_length: int = 8000) -> str:
    """Fetch a URL and extract readable content as Markdown.

    Args:
        url: The URL to fetch.
        max_length: Maximum content length in characters (1-32000).

    Returns:
        Formatted string with title, URL, and markdown content.
    """
    max_length = max(1, min(max_length, 32000))

    try:
        with httpx.Client(
            follow_redirects=True,
            timeout=30,
            headers={"User-Agent": _USER_AGENT},
        ) as client:
            resp = client.get(url)
            resp.raise_for_status()
    except httpx.TimeoutException:
        return f"Fetch failed: request timed out after 30s for {url}"
    except httpx.HTTPStatusError as e:
        return f"Fetch failed: HTTP {e.response.status_code} for {url}"
    except Exception as e:
        return f"Fetch failed: {e}"

    # Extract title from HTML
    title = ""
    content_type = resp.headers.get("content-type", "")
    raw_html = resp.text

    if "html" in content_type.lower():
        # Simple title extraction
        lower = raw_html.lower()
        start = lower.find("<title")
        if start != -1:
            start = lower.find(">", start) + 1
            end = lower.find("</title>", start)
            if end != -1:
                title = raw_html[start:end].strip()

        # Convert HTML to Markdown
        h = html2text.HTML2Text()
        h.ignore_images = True
        h.ignore_emphasis = False
        h.body_width = 0  # No line wrapping
        text = h.handle(raw_html)
    else:
        # Non-HTML content, return as-is
        text = raw_html

    truncated = False
    if len(text) > max_length:
        text = text[:max_length]
        truncated = True

    lines = []
    if title:
        lines.append(f"Title: {title}")
    lines.append(f"URL: {resp.url}")
    lines.append(f"Content-Length: {len(text)} chars")
    lines.append("")
    lines.append(text)
    if truncated:
        lines.append(f"\n(content truncated at {max_length} chars)")

    return "\n".join(lines)
