#!/usr/bin/env python3
"""Joins the files under src/ into one page, index.html.

    python3 web/mock/build.py

- src/page.html is the page. The styles, the sprite and the script go in
  place of /*@css*/, <!--@sprite--> and /*@js*/.
- src/css/*.css are joined in the order of their names.
- The modules are collected from src/js/main.js along its imports. Each is
  wrapped in a function, so that other modules see only what it exports. The
  build fails if a module imports a name that the other does not export.

Of the syntax of ES modules, the modules use only

    import {a, b as c} from './x.js';
    export function f() {}      export async function f() {}
    export const x = ...;       export class C {}
"""

import os
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent
SRC = ROOT / "src"

IMPORT = re.compile(r"^import\s*\{([^}]*)\}\s*from\s*'(\.[^']+)';?[ \t]*$", re.M)
EXPORT = re.compile(r"^export\s+((?:async\s+)?function\*?|const|let|class)\s+([A-Za-z_$][\w$]*)", re.M)
OTHER = re.compile(r"^\s*(import|export)\b.*$", re.M)


def fail(message):
    sys.exit(f"build: {message}")


def load(path, modules, order, stack):
    key = path.relative_to(SRC).as_posix()
    if key in modules:
        if key in stack:
            fail(f"import cycle: {' -> '.join(stack + [key])}")
        return key
    if not path.is_file():
        fail(f"no such module: {key} (imported by {stack[-1] if stack else 'the build'})")
    source = path.read_text()
    modules[key] = None
    imports = []
    for match in IMPORT.finditer(source):
        names = [part.strip() for part in match.group(1).split(",") if part.strip()]
        target = load((path.parent / match.group(2)).resolve(), modules, order, stack + [key])
        imports.append((names, target))
    exports = [match.group(2) for match in EXPORT.finditer(source)]
    body = IMPORT.sub("", source)
    body = EXPORT.sub(lambda m: f"{m.group(1)} {m.group(2)}", body)
    leftover = OTHER.search(body)
    if leftover:
        fail(f"{key}: unsupported module syntax: {leftover.group(0).strip()}")
    modules[key] = (imports, exports, body)
    order.append(key)
    return key


def bundle():
    modules, order = {}, []
    load(SRC / "js" / "main.js", modules, order, [])
    out = ["const __mod = {};"]
    for key in order:
        imports, exports, body = modules[key]
        head = []
        for names, target in imports:
            offered = modules[target][1]
            fields = []
            for name in names:
                original, _, local = (part.strip() for part in name.partition(" as "))
                if original not in offered:
                    fail(f"{key}: {target} does not export {original}")
                fields.append(f"{original}: {local}" if local else original)
            head.append(f"const {{{', '.join(fields)}}} = __mod[{target!r}];")
        out.append(
            f"// ---- {key}\n__mod[{key!r}] = (function () {{\n{chr(10).join(head)}\n{body.strip()}\n"
            f"return {{{', '.join(exports)}}};\n}})();"
        )
    return "(function () {\n'use strict';\n" + "\n\n".join(out) + "\n})();"


def main():
    page = (SRC / "page.html").read_text()
    css = "\n".join(path.read_text().strip() for path in sorted((SRC / "css").glob("*.css")))
    sprite = (SRC / "sprite.svg").read_text().strip()
    script = bundle().replace("</script", "<\\/script")
    for mark, text in (("/*@css*/", css), ("<!--@sprite-->", sprite), ("/*@js*/", script)):
        if page.count(mark) != 1:
            fail(f"page.html must contain {mark} exactly once")
        page = page.replace(mark, text)
    target = ROOT / "index.html"
    target.write_text(page)
    print(f"{os.path.relpath(target)}: {len(page.encode()) / 1024:.0f} KiB")


if __name__ == "__main__":
    main()
