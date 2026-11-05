"""Create a fictional PDF fixture and render actual PDF pages for native reading."""
from pathlib import Path
import base64
import hashlib
import json
import fitz

root = Path(__file__).resolve().parent / 'fixtures'
root.mkdir(parents=True, exist_ok=True)
pdf_path = root / 'fictional-research-guide-v1.pdf'
doc = fitz.open()
sections = [
    ('研究内容填写说明', [
        ('一、研究对象', '围绕拟研究的问题，明确研究对象及其范围。社区数字服务的可及性研究可以关注服务入口、使用过程与支持机制。'),
        ('二、总体框架', '研究内容应与研究目标相对应，说明各部分之间的关系，并呈现总体研究框架。不同部分不应只是材料的简单罗列。'),
        ('三、重点难点', '结合研究对象说明需要重点解决的问题，并区分资料获取、分析方法与实践条件带来的不同困难。'),
    ]),
    ('研究方法与资料核对', [
        ('一、方法适配', '结合研究对象说明基本思路、具体方法及研究计划。访谈与观察可以帮助理解不同群体使用社区服务时遇到的具体障碍。'),
        ('二、出处核对', '阅读资料时应回到原文，核对引文的前后语境、版本和页码。资料中的陈述不应直接视为已经验证的研究结论。'),
        ('三、工作记录', '记录资料版本及本次阅读位置。正文修改由作者决定，资料阅读不应改变正文的未保存内容和撤销历史。'),
    ]),
]
for number, (title, paragraphs) in enumerate(sections, 1):
    page = doc.new_page(width=595, height=842)
    page.insert_text((55, 45), 'LingDoc · 原生资料阅读验证', fontname='china-s', fontsize=10, color=(.40, .44, .50))
    page.insert_text((55, 86), title, fontname='china-s', fontsize=20)
    page.insert_text((55, 113), '虚构演示资料 · 非正式申报规定 · v1', fontname='china-s', fontsize=10, color=(.55, .25, .15))
    y = 160
    for heading, body in paragraphs:
        page.insert_text((55, y), heading, fontname='china-s', fontsize=14)
        remaining = page.insert_textbox(fitz.Rect(55, y + 21, 540, y + 140), body,
                                       fontname='china-s', fontsize=13, lineheight=1.8)
        if remaining < 0:
            raise RuntimeError('PDF fixture text overflow')
        y += 180
    page.insert_text((274, 803), f'{number} / 2', fontsize=10)
doc.set_metadata({'title': '虚构研究说明 v1', 'subject': 'Native reading test fixture; not official requirements', 'author': 'LingDoc test fixture'})
doc.save(pdf_path, garbage=4, deflate=True)
doc.close()
pages = []
page_facts = []
with fitz.open(pdf_path) as source:
    for index, page in enumerate(source):
        pix = page.get_pixmap(matrix=fitz.Matrix(1.5, 1.5), alpha=False)
        png = pix.tobytes('png')
        (root / f'page-{index + 1}.png').write_bytes(png)
        pages.append(base64.b64encode(png).decode('ascii'))
        page_facts.append({'page': index + 1, 'width': pix.width, 'height': pix.height,
                           'sha256': hashlib.sha256(png).hexdigest(), 'text': page.get_text()})
pdf_sha = hashlib.sha256(pdf_path.read_bytes()).hexdigest()
payload = {'op': 'source.open', 'sourceId': 'fictional-research-guide', 'title': '研究说明.pdf（演示）',
           'version': 'v1', 'pages': pages}
(root / 'source.json').write_text(json.dumps(payload, ensure_ascii=False), encoding='utf-8')
evidence = {'renderer': 'PyMuPDF ' + fitz.VersionBind, 'source': pdf_path.name, 'pdfSha256': pdf_sha,
            'fictional': True, 'originalPdfRetained': True, 'pageCount': len(pages), 'pages': page_facts,
            'limitations': ['Raster page viewing; no PDF text selection or text search is claimed.']}
(root / 'manifest.json').write_text(json.dumps(evidence, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps({'pdf': str(pdf_path), 'sha256': pdf_sha, 'pages': len(pages)}, ensure_ascii=False))
