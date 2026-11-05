"""Create an isolated, fictional draft using the existing native section format."""
import sys
import zipfile
import xml.etree.ElementTree as ET
from copy import deepcopy

source, target = sys.argv[1:]
ns = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
ET.register_namespace('w', ns)
tag = lambda name: '{' + ns + '}' + name
with zipfile.ZipFile(source) as original:
    root = ET.fromstring(original.read('word/document.xml'))
    body = root.find(tag('body'))
    tokens = ['[选题说明]', '[选题依据]', '[研究内容]', '[创新之处]', '[预期成果]', '[参考文献]']
    headers = []
    for token in tokens:
        headers.append(deepcopy(next(p for p in body.iter(tag('p')) if ''.join(p.itertext()).startswith(token))))
    section = deepcopy(body.find(tag('sectPr')))
    body.clear()
    def para(parent, text, bold=False):
        p = ET.SubElement(parent, tag('p'))
        run = ET.SubElement(p, tag('r'))
        props = ET.SubElement(run, tag('rPr'))
        ET.SubElement(props, tag('rFonts'), {tag('ascii'): 'Times New Roman', tag('eastAsia'): '宋体'})
        ET.SubElement(props, tag('sz'), {tag('val'): '24'})
        if bold: ET.SubElement(props, tag('b'))
        ET.SubElement(run, tag('t')).text = text
    para(body, '社区老年人数字公共服务使用障碍研究', True)
    para(body, '虚构科研申报试用稿 · 所有正文均可直接在 WordCraft 中编辑。')
    table = ET.SubElement(body, tag('tbl'))
    grid = ET.SubElement(table, tag('tblGrid'))
    ET.SubElement(grid, tag('gridCol'), {tag('w'): '9000'})
    cell = ET.SubElement(ET.SubElement(table, tag('tr')), tag('tc'))
    texts = [
        ['关注社区老年人接触数字公共服务时遇到的困难，以及可获得的支持。'],
        ['本次研究条件依据社区联系记录 CONTACT-v1：四个社区表示愿意协助联系与招募。'],
        ['样本选择：计划进入四个社区开展问卷与访谈，据此了解全市老年人使用数字公共服务的情况。',
         '进度安排：首月完成四个社区的联系与招募，随后开展问卷与访谈。',
         '访谈方法：采用半结构访谈，围绕服务接触、使用障碍与支持需求开展交流。'],
        ['结合接触经历和支持条件整理使用障碍，具体创新判断仍需研究者论证。'],
        ['形成阶段性研究报告。本试用稿未达到正式申报交付条件。'],
        ['当前仅登记内部联系记录，正式参考文献尚待研究者补充。'],
    ]
    for header, lines in zip(headers, texts):
        cell.append(header)
        for line in lines: para(cell, line)
    if section is not None: body.append(section)
    with zipfile.ZipFile(target, 'w', zipfile.ZIP_DEFLATED) as output:
        for item in original.infolist():
            output.writestr(item, ET.tostring(root, encoding='utf-8', xml_declaration=True) if item.filename == 'word/document.xml' else original.read(item.filename))
