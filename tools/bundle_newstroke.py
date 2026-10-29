#!/usr/bin/env python3
"""Bundle KiCad's encoded glyphs without requiring KiCad or Python at runtime.

Download SOURCE below, then run: python3 tools/bundle_newstroke.py newstroke_font.cpp
"""
import ast
import gzip
import hashlib
from pathlib import Path
import re
import sys

REVISION = "4d0814cfafc6e120a376fe418fcf5cfd15b74e97"
SOURCE = f"https://raw.githubusercontent.com/KiCad/kicad-source-mirror/{REVISION}/common/newstroke_font.cpp"

source = Path(sys.argv[1]).read_text()
assert hashlib.sha256(source.encode()).hexdigest() == "68b3c587f516bab095feadf264a6cb4fe0206f2bc6ea0a64a37f3179e51b551a"
header, body = source.split("const char* const newstroke_font[] =", 1)
body = body.split("};", 1)[0]
glyphs = [ast.literal_eval(m) for m in re.findall(r'^\s*("(?:[^"\\]|\\.)*")\s*,', body, re.M)]
assert len(glyphs) == 65487
assert all(len(g) >= 2 and len(g) % 2 == 0 and "\n" not in g for g in glyphs)
dest = Path(__file__).resolve().parents[1] / "fonts/newstroke"
dest.mkdir(parents=True, exist_ok=True)
(dest / "glyphs.gz").write_bytes(gzip.compress("\n".join(glyphs).encode("ascii"), mtime=0))
(dest / "NOTICE.txt").write_text(
    "KiCad Newstroke font data\n\nSource: " + SOURCE + "\n"
    "SHA-256 of upstream file: " + hashlib.sha256(source.encode()).hexdigest() + "\n\n"
    "Bundled as gzip-compressed, newline-separated Hershey glyph strings, starting at U+0020.\n"
    "Regenerate with tools/bundle_newstroke.py. No glyph shapes have been changed.\n"
    "The GPL-covered portions are distributed under GPL version 3, as permitted by\n"
    "the upstream GPL-2.0-or-later grant. See the project's LICENSE for its full text.\n"
    "The MIT and SIL OFL notices and terms below continue to apply to their respective portions.\n"
    "Upstream tools/newstroke/README.txt also identifies the original Newstroke font\n"
    "by Vladimir Uryvaev as released under CC0. The combined data retains all notices below.\n\n"
    "Unmodified upstream notices and license terms:\n\n" + header.split("#include", 1)[0])
