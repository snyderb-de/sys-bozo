#!/usr/bin/env python3
"""Build a visual gallery from TestRenderStudioGallery's actual ANSI output.

BOZO_RENDER_DIR="$PWD/.tmp/tui-preview" go test ./internal/tui -run TestRenderStudioGallery
python3 scripts/tui-preview.py .tmp/tui-preview/screens.json
"""
import html
import json
import re
import sys
from pathlib import Path


def render_ansi(text):
    output, styles = [], {}
    tokens = re.split(r"(\x1b\[[0-9;]*m)", text)
    for token in tokens:
        if token.startswith("\x1b["):
            codes = [int(c) for c in token[2:-1].split(";") if c] or [0]
            i = 0
            while i < len(codes):
                code = codes[i]
                if code == 0:
                    styles = {}
                elif code == 1:
                    styles["font-weight"] = "700"
                elif code == 22:
                    styles.pop("font-weight", None)
                elif code in (38, 48) and codes[i+1:i+2] == [2]:
                    styles["color" if code == 38 else "background-color"] = "#%02x%02x%02x" % tuple(codes[i+2:i+5])
                    i += 4
                elif code in (39, 49):
                    styles.pop("color" if code == 39 else "background-color", None)
                i += 1
        else:
            css = ";".join(f"{key}:{value}" for key, value in styles.items())
            escaped = html.escape(token)
            escaped = re.sub(r"([\u2500-\u259f])", r'<span class="cell">\1</span>', escaped)
            output.append(f'<span style="{css}">{escaped}</span>')
    return "".join(output)


source = Path(sys.argv[1])
captures = json.loads(source.read_text())
sections = []
for capture in captures:
    name = html.escape(capture["Name"])
    sections.append(f'<section><h2>{name} <small>{capture["Width"]} × {capture["Height"]}</small></h2><pre>{render_ansi(capture["ANSI"])}</pre></section>')
page = '''<!doctype html><html lang="en"><meta charset="utf-8"><title>sys-bozo · terminal gallery</title><link rel="icon" href="data:,">
<style>*{box-sizing:border-box}body{margin:40px;background:#11161d;color:#e4eaf2;font:16px system-ui}h1{font-size:32px;margin-bottom:8px}p,small{color:#95a3b6}small{font-size:14px;font-weight:400}section{margin:40px 0;overflow:auto}pre{display:inline-block;padding:22px;background:#171c24;border:1px solid #394658;border-radius:8px;font:14px/1.45 Menlo,monospace;white-space:pre;margin:0}h2{font-size:18px;color:#8bbcff}.cell{display:inline-block;width:1ch;text-align:center;font-weight:400}</style>
<h1>sys-bozo</h1><p>Terminal redesign · actual application renders with fictional fixture data.</p>'''+"\n".join(sections)
source.with_name("index.html").write_text(page)
print(source.with_name("index.html"))
