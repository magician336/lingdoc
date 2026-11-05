"""Bounded local extraction of explicitly uploaded copies. No network or model use."""
import hashlib
import json
import math
from pathlib import Path
import sys
import zipfile
import xml.etree.ElementTree as ET

MAX_TEXT = 2 * 1024 * 1024

def write_derived(destination, data):
    if destination.is_symlink() or (destination.exists() and not destination.is_file()):
        raise ValueError('解析产物路径不是普通文件，停止写入')
    destination.write_bytes(data)

def bounded_text(text):
    if '\x00' in text or len(text.encode('utf-8')) > MAX_TEXT:
        raise ValueError('资料文本包含空字节或超过 2 MiB 阅读上限')
    return text

def extract(source, output, kind):
    output.mkdir(parents=True, exist_ok=True)
    result = {'rendering': 'plain-text', 'pages': [], 'pageTexts': []}
    if kind in ('txt', 'md'):
        data = source.read_bytes()
        codec = 'utf-16' if data.startswith((b'\xff\xfe', b'\xfe\xff')) else 'utf-8-sig'
        try:
            text = bounded_text(data.decode(codec, errors='strict'))
        except UnicodeDecodeError:
            raise ValueError('文本编码无法识别，请另存为 UTF-8 或带 BOM 的 UTF-16 后重试')
    elif kind == 'docx':
        with zipfile.ZipFile(source) as archive:
            entries = archive.infolist()
            if len(entries) > 2048 or sum(e.file_size for e in entries) > 64 * 1024 * 1024:
                raise ValueError('DOCX 展开内容超过安全读取上限')
            for entry in entries:
                if entry.file_size > 16 * 1024 * 1024 or (entry.file_size > 1024 * 1024 and entry.file_size > max(entry.compress_size, 1) * 1000):
                    raise ValueError('DOCX 条目展开比例或大小超限')
            if '[Content_Types].xml' not in archive.namelist() or 'word/document.xml' not in archive.namelist():
                raise ValueError('文件不是有效 DOCX')
            xml = archive.read('word/document.xml')
            if b'<!DOCTYPE' in xml.upper() or b'<!ENTITY' in xml.upper():
                raise ValueError('DOCX XML 不允许 DTD 或实体声明')
            root = ET.fromstring(xml)
            ns = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'
            body = root.find(ns + 'body')
            if body is None:
                raise ValueError('DOCX 缺少正文')
            lines = []
            # Paragraph traversal follows document order, including paragraphs inside tables.
            for paragraph in body.iter(ns + 'p'):
                parts = []
                for node in paragraph.iter():
                    if node.tag == ns + 't': parts.append(node.text or '')
                    elif node.tag == ns + 'tab': parts.append('\t')
                    elif node.tag in (ns + 'br', ns + 'cr'): parts.append('\n')
                lines.append(''.join(parts))
            text = bounded_text('\n'.join(lines))
            result['rendering'] = 'docx-extracted-text'
            result['limitations'] = ['DOCX 解析文本，不保留原版式；本轮读取正文段落与表格，不含页眉、脚注及图片内容。']
    elif kind == 'pdf':
        import fitz
        with fitz.open(source) as document:
            if document.needs_pass:
                raise ValueError('加密 PDF 暂不支持，请先提供可阅读副本')
            if not 1 <= len(document) <= 32:
                raise ValueError('PDF 预览支持 1–32 页，原文件已保留')
            page_texts = []
            for index, page in enumerate(document):
                rect = page.rect
                if not all(math.isfinite(v) and v > 0 for v in (rect.width, rect.height)) or max(rect.width, rect.height) > 8192:
                    raise ValueError('PDF 页面尺寸超限')
                page_texts.append({'page': index + 1, 'text': bounded_text(page.get_text())})
            text = bounded_text('\n\n'.join(f'【第 {p["page"]} 页】\n{p["text"]}' for p in page_texts))
            raw_text = ''.join(p['text'] for p in page_texts)
            if not raw_text.strip(): text = ''
            result['pageTexts'] = page_texts
            empty_pages = sum(not p['text'].strip() for p in page_texts)
            if empty_pages and raw_text.strip():
                result['notice'] = f'{empty_pages} 页没有可提取文字；问答只覆盖已有文字层，未执行 OCR。'
            rendered = None
            for scale in (1.0, 0.75, 0.5):
                candidate = []
                pixels = 0
                encoded_size = 0
                for page in document:
                    pixels += math.ceil(page.rect.width * scale) * math.ceil(page.rect.height * scale)
                    if pixels > 32 * 1024 * 1024:
                        break
                    pixmap = page.get_pixmap(matrix=fitz.Matrix(scale, scale), alpha=False)
                    data = pixmap.tobytes('png')
                    encoded_size += 4 * math.ceil(len(data) / 3)
                    if len(data) > 3 * 1024 * 1024 or encoded_size > 7 * 1024 * 1024:
                        break
                    candidate.append(data)
                if len(candidate) == len(document):
                    rendered = candidate
                    break
            if rendered is None:
                raise ValueError('PDF 页面渲染超过本轮预览上限，原文件已保留')
            for i, data in enumerate(rendered, 1):
                name = f'page-{i}.png'
                write_derived(output / name, data)
                result['pages'].append({'file': name, 'sha256': hashlib.sha256(data).hexdigest()})
            result['rendering'] = 'host-rendered-png'
            result['limitations'] = ['PDF 页图只读；不支持选字或全文搜索。']
    else:
        raise ValueError('不支持的资料格式')
    write_derived(output / 'text.txt', text.encode('utf-8'))
    result['textFile'] = 'text.txt'
    result['textSha256'] = hashlib.sha256(text.encode('utf-8')).hexdigest()
    result['searchable'] = bool(text.strip())
    if not result['searchable']:
        result['notice'] = '仅可阅读：未提取到可检索文字，未执行 OCR 或模型调用。'
    write_derived(output / 'extraction.json', json.dumps(result, ensure_ascii=False).encode('utf-8'))
    return result

if __name__ == '__main__':
    try:
        original, destination, extension = sys.argv[1:]
        if Path(original).stat().st_size > 10 * 1024 * 1024:
            raise ValueError('资料超过 10 MiB')
        answer = extract(Path(original), Path(destination), extension)
        print(json.dumps({'ok': True, 'rendering': answer['rendering'], 'searchable': answer['searchable']}, ensure_ascii=False))
    except Exception as error:
        print(json.dumps({'ok': False, 'error': str(error)}, ensure_ascii=False))
        sys.exit(1)
