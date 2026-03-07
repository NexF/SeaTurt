"""web_search tool implementation using ddgs."""

from ddgs import DDGS


def web_search(
    query: str,
    max_results: int = 5,
    search_depth: str = "basic",
) -> str:
    """Search the web using DuckDuckGo and return formatted results.

    Args:
        query: Search query string.
        max_results: Maximum number of results (1-10).
        search_depth: "basic" or "advanced" (advanced fetches more content).

    Returns:
        Formatted markdown string of search results.
    """
    max_results = max(1, min(max_results, 10))

    try:
        results = DDGS().text(
            query,
            max_results=max_results,
            backend="auto",
        )
    except Exception as e:
        return f"Search failed: {e}"

    if not results:
        return f'No results found for: "{query}"'

    lines = [f'Search results for: "{query}"\n']
    for i, r in enumerate(results, 1):
        title = r.get("title", "Untitled")
        href = r.get("href", "")
        body = r.get("body", "")
        lines.append(f"{i}. [{title}]({href})")
        if body:
            lines.append(f"   {body}")
        lines.append("")

    lines.append(f"({len(results)} results)")
    return "\n".join(lines)
