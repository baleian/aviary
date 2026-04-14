from urllib.parse import quote

from fastapi.responses import RedirectResponse


def ok(url: str, message: str) -> RedirectResponse:
    sep = "&" if "?" in url else "?"
    return RedirectResponse(f"{url}{sep}flash={quote(message)}", status_code=303)


def err(url: str, message: str) -> RedirectResponse:
    sep = "&" if "?" in url else "?"
    return RedirectResponse(f"{url}{sep}error={quote(message)}", status_code=303)
