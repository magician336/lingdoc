//! Document grid (`w:docGrid`): line pitch and character pitch (#391). Assertions are relations
//! to the pitch, never font metrics (fonts differ between machines; CJK glyphs may be missing).

use super::*;
use wordcraft_doc::props::LineSpacing;
use wordcraft_doc::section::{DocGrid, DocGridType};

fn lay(doc: &Document) -> DocLayout {
    layout(doc, &mut LayoutCache::new(), &LayoutOptions::default())
}

fn with_grid(text: &str, kind: DocGridType, char_space: i32) -> Document {
    let mut d = Document::from_text(text);
    for b in d.body.iter_mut() {
        if let Block::Para(p) = std::sync::Arc::make_mut(b) {
            p.props.space_before = Some(0.0);
            p.props.space_after = Some(0.0);
            p.props.line_spacing = Some(LineSpacing::Multiple(1.0));
        }
    }
    d.last_section.doc_grid = Some(DocGrid { kind, line_pitch: 18.0, char_space });
    d
}

/// (first-page lines, every line height) of a layout.
fn lines(l: &DocLayout) -> (usize, Vec<f32>) {
    let mut first = 0;
    let mut hs = Vec::new();
    for (pi, p) in l.pages.iter().enumerate() {
        for it in &p.items {
            if let Placed::Lines { para, l0, l1, .. } = it {
                if pi == 0 {
                    first += l1 - l0;
                }
                hs.extend(para.lines[*l0..*l1].iter().map(|line| line.height));
            }
        }
    }
    (first, hs)
}

#[test]
fn grid_multiple_spacing_does_not_double_the_font_grid_floor() {
    // Black-box Word 16 probe: a 16pt Chinese heading, 15.6pt grid and
    // double spacing advances by two pitches, not four pitches.
    let mut d = with_grid("一、数据表", DocGridType::Lines, 0);
    d.last_section.doc_grid.as_mut().unwrap().line_pitch = 15.6;
    let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
    p.mark.font = Some("黑体".into());
    p.mark.size = Some(16.0);
    for run in &mut p.runs {
        run.props.font = Some("黑体".into());
        run.props.size = Some(16.0);
    }
    p.props.line_spacing = Some(LineSpacing::Multiple(2.0));
    let (_, hs) = lines(&lay(&d));
    assert!((hs[0] - 31.2).abs() < 0.05, "double-grid heading height {}", hs[0]);
}

#[test]
fn grid_spacing_keeps_a_tall_font_floor_and_single_spacing() {
    let g = para::Grid { line: Some(15.6), ..Default::default() };
    for (natural, multiple, expected) in [
        (15.45, 1.0, 15.6),
        (15.45, 2.0, 31.2),
        (20.55, 1.0, 31.2),
        (20.55, 2.0, 31.2),
        (41.15, 1.0, 46.8),
        (41.15, 2.0, 46.8),
    ] {
        assert!((g.multiple_height(natural, multiple) - expected).abs() < 0.01);
        assert!(g.multiple_height(natural, multiple) >= natural);
    }
    assert!((para::Grid::default().multiple_height(20.55, 2.0) - 41.1).abs() < 0.01);
}

#[test]
fn table_grid_does_not_enlarge_cell_text_or_blank_paragraphs() {
    // Word 16 black-box control: section line grids govern body paragraphs,
    // while cell paragraphs keep their natural single spacing.
    for text in ["甲\n\n乙", "甲\n \n乙"] {
        let mut d = with_grid(text, DocGridType::Lines, 0);
        d.last_section.doc_grid.as_mut().unwrap().line_pitch = 30.0;
        let blocks = std::mem::take(&mut d.body);
        let mut table = wordcraft_doc::Table::new(1, 1, 468.0);
        table.props.cell_margins = Some([0.0; 4]);
        table.rows[0].cells[0].blocks = blocks;
        d.body = vec![std::sync::Arc::new(Block::Table(table))];
        let mut plain = d.clone();
        plain.last_section.doc_grid = None;
        let (_, hs) = lines(&lay(&d));
        let (_, natural) = lines(&lay(&plain));
        assert_eq!(hs, natural, "cell grid inflated {text:?}");
        assert!(hs.iter().all(|h| *h < 30.0));
    }
    let mut body = with_grid("甲\n\n乙", DocGridType::Lines, 0);
    body.last_section.doc_grid.as_mut().unwrap().line_pitch = 30.0;
    assert!(lines(&lay(&body)).1.iter().all(|h| (*h - 30.0).abs() < 0.01));
}

fn terminal_table_grid_document(height: f32, text: &str) -> Document {
    use wordcraft_doc::props::HeightRule;
    let mut d = Document::new();
    d.last_section.page_h = 792.0;
    d.last_section.margin_top = 72.0;
    d.last_section.margin_bottom = 72.0;
    d.last_section.doc_grid = Some(DocGrid { kind: DocGridType::Lines, line_pitch: 18.0, char_space: 0 });
    let mut table = wordcraft_doc::Table::new(1, 1, 468.0);
    table.props.style = None;
    table.props.cell_margins = Some([0.0; 4]);
    table.props.borders = Some(Default::default());
    table.rows[0].props.height = Some(height);
    table.rows[0].props.height_rule = HeightRule::Exact;
    let mut mark = wordcraft_doc::Paragraph::with_text(text, wordcraft_doc::CharProps {
        font: Some("Times New Roman".into()), size: Some(10.5), ..Default::default()
    });
    mark.mark.font = Some("Times New Roman".into());
    mark.mark.size = Some(10.5);
    mark.props.space_before = Some(0.0);
    mark.props.space_after = Some(0.0);
    mark.props.line_spacing = Some(LineSpacing::Multiple(2.0));
    d.body = vec![std::sync::Arc::new(Block::Table(table)), wordcraft_doc::para_block(mark)];
    d
}

#[test]
fn final_table_mark_keeps_its_grid_line_without_an_empty_tail_page() {
    for text in ["", "甲"] {
        let l = lay(&terminal_table_grid_document(620.0, text));
        assert_eq!(l.pages.len(), 1, "the final grid box alone must not add a tail page: {text:?}");
        let (_, hs) = lines(&l);
        assert!((hs.last().unwrap() - 36.0).abs() < 0.01, "its grid line is preserved: {hs:?}");
        assert!(l.caret(&wordcraft_doc::Pos::body(1, 0)).is_some(), "the final paragraph is kept and reachable");
    }
}

#[test]
fn final_table_grid_line_still_breaks_when_its_text_does_not_fit() {
    for height in [632.0, 640.0] {
        assert_eq!(lay(&terminal_table_grid_document(height, "")).pages.len(), 2, "remaining {}pt", 648.0-height);
    }
    let mut explicit = terminal_table_grid_document(620.0, "");
    explicit.para_mut(StoryRef::Body, &Path::top(1)).unwrap().props.page_break_before = Some(true);
    assert_eq!(lay(&explicit).pages.len(), 2, "an explicit page break remains effective");
}

#[test]
fn a_single_grid_line_keeps_its_baseline_on_the_page_before_a_following_title() {
    // The real form has a 16pt empty grid paragraph at the end of the cover,
    // followed by its commitment title. Word keeps the empty paragraph on
    // the cover, rather than adding its whole grid box before the title.
    let mut d = terminal_table_grid_document(620.0, "");
    d.body.push(wordcraft_doc::para_block(wordcraft_doc::Paragraph::with_text(
        "Following title", wordcraft_doc::CharProps::default(),
    )));
    let l = lay(&d);
    let page_of = |block| l.pages.iter().position(|page| page.items.iter().any(|item| {
        matches!(item, Placed::Lines { path, .. } if path.0.as_slice() == [block])
    }));
    assert_eq!(page_of(1), Some(0), "the lower grid leading must not move the preceding line");
    assert_eq!(page_of(2), Some(1), "the following title still moves normally");
}

#[test]
fn a_single_grid_line_keeps_a_bookmark_with_its_preceding_page() {
    let mut d = terminal_table_grid_document(620.0, "");
    d.para_mut(StoryRef::Body, &Path::top(1)).unwrap().insert_object(
        0, InlineObject::BookmarkEnd { name: "cover".into() }, &Default::default(),
    ).unwrap();
    d.body.push(wordcraft_doc::para_block(wordcraft_doc::Paragraph::with_text(
        "Following title", wordcraft_doc::CharProps::default(),
    )));
    let l = lay(&d);
    assert!(l.pages[0].items.iter().any(|item| {
        matches!(item, Placed::Lines { path, .. } if path.0.as_slice() == [1])
    }), "a zero-width bookmark must not turn the cover's last grid paragraph into a new-page line");
}

#[test]
fn punctuation_compression_fits_a_group_without_condensing_unbroken_lines() {
    use wordcraft_doc::CharacterSpacingControl as Compression;
    use wordcraft_doc::props::Align;
    if !wordcraft_fonts::FontDb::global().has_family("楷体") { return; }
    let text = "甲，乙。丙：丁；戊！己？庚《辛》壬（癸）";
    let make = |width: f32, compression, align| {
        let mut d = Document::from_text(text);
        d.settings.character_spacing_control = compression;
        d.last_section.page_w = width + 144.0;
        d.last_section.margin_left = 72.0;
        d.last_section.margin_right = 72.0;
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.mark.font = Some("楷体".into()); p.mark.size = Some(14.0);
        p.props.align = Some(align);
        p.props.line_spacing = Some(LineSpacing::Exactly(24.0));
        for r in &mut p.runs { r.props.font = Some("楷体".into()); r.props.size = Some(14.0); }
        d
    };
    let first_line = |d: &Document| {
        let l = lay(d);
        l.pages[0].items.iter().find_map(|item| if let Placed::Lines { para, .. } = item {
            para.lines.first().map(|line| (text[..line.stop].to_owned(), line.xs.clone()))
        } else { None }).unwrap()
    };
    for align in [Align::Left, Align::Justify] {
        let plain = first_line(&make(192.0, Compression::DoNotCompress, align));
        assert_eq!(plain.0, "甲，乙。丙：丁；戊！己？庚");
        for compression in [Compression::CompressPunctuation, Compression::CompressPunctuationAndJapaneseKana] {
            let compressed = first_line(&make(192.0, compression, align));
            assert_eq!(compressed.0, "甲，乙。丙：丁；戊！己？庚《辛》", "actual Word 16 narrow control");
            let wide = first_line(&make(468.0, compression, align));
            assert_eq!(wide.0, text);
            assert_eq!(wide.1, first_line(&make(468.0, Compression::DoNotCompress, align)).1,
                "a fitting line keeps its original character spacing");
        }
    }
}

#[test]
fn character_start_uses_normal_size_and_first_line_uses_the_direct_mark_size() {
    for style in ["Normal", "Heading1"] {
        for (chars, left, first) in [(1.0, 22.0, 12.0), (-1.0, 34.0, -12.0)] {
            let mut d = Document::from_text("甲");
            d.styles.get_mut("Normal").unwrap().chr.size = Some(11.0);
            d.styles.get_mut("Heading1").unwrap().chr.size = Some(16.0);
            let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
            p.props.style = Some(style.into());
            p.props.indent_left_chars = Some(2.0);
            p.props.indent_first_chars = Some(chars);
            p.mark.size = Some(12.0);
            for r in &mut p.runs { r.props.size = Some(12.0); }
            let l = lay(&d);
            let rp = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(&para.rp) } else { None }).unwrap();
            assert!((rp.indent_left-left).abs()<0.01, "Word's Normal11/style16/mark12 start: {style} {chars}: {}",rp.indent_left);
            assert!((rp.indent_first-first).abs()<0.01);
        }
    }
}

#[test]
fn punctuation_fitting_keeps_an_ordinary_overflowing_character_on_the_next_line() {
    if !wordcraft_fonts::FontDb::global().has_family("宋体") { return; }
    for (text, size, width, expected) in [
        ("3.《数据表》的填写和录入请参阅《填写<数据表>注意事项》，相关问题可咨询省级社科管理部门。", 10.5, 443.94995, "3.《数据表》的填写和录入请参阅《填写<数据表>注意事项》，相关问题可咨询省级社科管理部"),
        ("甲乙丙丁戊己庚辛壬癸子丑寅。", 14.0, 192.0, "甲乙丙丁戊己庚辛壬癸子丑寅。"),
    ] {
        let mut d = Document::from_text(text);
        d.settings.character_spacing_control = wordcraft_doc::CharacterSpacingControl::CompressPunctuation;
        d.last_section.page_w = width + 144.0;
        d.last_section.margin_left = 72.0; d.last_section.margin_right = 72.0;
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.mark.font = Some("宋体".into()); p.mark.size = Some(size);
        p.props.line_spacing = Some(LineSpacing::Exactly(24.0));
        for r in &mut p.runs { r.props.font = Some("宋体".into()); r.props.size = Some(size); }
        let l = lay(&d);
        let stop = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para.lines[0].stop) } else { None }).unwrap();
        assert_eq!(&text[..stop], expected, "actual Word ordinary/closing overflow controls");
    }
}

#[test]
fn fixed_line_spacing_uses_the_lines_baseline_across_cjk_and_latin_faces() {
    // Word 16's generated EMF: 24pt exact lines at top 85.05 draw at
    // logical y869 (600dpi) for both KaiTi and Times New Roman; 19pt
    // exact Song draws at y836. The baseline belongs to the fixed line.
    for (family, size, height, text) in [
        ("楷体", 14.0, 24.0, "甲乙"),
        ("Times New Roman", 14.0, 24.0, "Ab"),
        ("宋体", 10.5, 19.0, "甲乙"),
    ] {
        let mut d = Document::from_text(text);
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.mark.font = Some(family.into()); p.mark.size = Some(size);
        p.props.line_spacing = Some(LineSpacing::Exactly(height));
        for r in &mut p.runs { r.props.font = Some(family.into()); r.props.size = Some(size); }
        let l = lay(&d);
        let baseline = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para.lines[0].baseline) } else { None }).unwrap();
        assert!((baseline - height * 0.8).abs() < 0.01, "fixed line baseline: {family}/{size} {baseline}");
    }
}

#[test]
fn grid_centres_the_font_without_its_external_leading() {
    if !wordcraft_fonts::FontDb::global().has_family("黑体") { return; }
    let mut d = with_grid("甲乙", DocGridType::Lines, 0);
    d.last_section.doc_grid.as_mut().unwrap().line_pitch = 15.6;
    let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
    p.mark.font = Some("黑体".into()); p.mark.size = Some(16.0);
    for r in &mut p.runs { r.props.font = Some("黑体".into()); r.props.size = Some(16.0); }
    let l = lay(&d);
    let baseline = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para.lines[0].baseline) } else { None }).unwrap();
    // Win metrics 220/-36 at 256upem, centred in a 31.2pt grid box.
    assert!((baseline - 21.35).abs() < 0.01, "Word grid baseline {baseline}");
}

#[test]
fn character_first_line_uses_the_first_actual_run_instead_of_the_mark() {
    for (mark, first, second) in [(10.5, 16.0, 16.0), (16.0, 10.5, 10.5), (11.0, 8.0, 16.0)] {
        let mut d = Document::from_text("甲乙");
        d.styles.get_mut("Normal").unwrap().chr.size = Some(11.0);
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.props.indent_left_chars = Some(2.0);
        p.props.indent_first_chars = Some(1.0);
        p.mark.size = Some(mark);
        p.runs = vec![
            wordcraft_doc::Run { len:3, props:wordcraft_doc::CharProps { size:Some(first), ..Default::default() } },
            wordcraft_doc::Run { len:3, props:wordcraft_doc::CharProps { size:Some(second), ..Default::default() } },
        ];
        let l = lay(&d);
        let rp = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(&para.rp) } else { None }).unwrap();
        assert!((rp.indent_left-22.0).abs()<0.01);
        assert!((rp.indent_first-first).abs()<0.01, "Word mark{mark}/first{first}/second{second}: {}",rp.indent_first);
    }
}

#[test]
fn justified_east_asian_lines_fill_the_measure_without_stretching_the_leading_spaces() {
    use wordcraft_doc::props::Align;
    if !wordcraft_fonts::FontDb::global().has_family("楷体") { return; }
    for prefix in ["", "  "] {
        let mut d = Document::from_text(&format!("{prefix}甲乙丙丁戊己庚辛壬癸子丑寅卯"));
        d.last_section.page_w = 244.0;
        d.last_section.margin_left = 72.0; d.last_section.margin_right = 72.0;
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.props.align = Some(Align::Justify);
        p.mark.font = Some("楷体".into()); p.mark.size = Some(14.0);
        for r in &mut p.runs { r.props.font=Some("楷体".into()); r.props.size=Some(14.0); }
        let l=lay(&d);
        let para=l.pages[0].items.iter().find_map(|it| if let Placed::Lines {para,..}=it {Some(para)} else {None}).unwrap();
        let first=&para.lines[0];
        assert!((first.xs.last().unwrap()-100.0).abs()<0.01,"a justified CJK line fills its measure");
        if !prefix.is_empty() { assert!((first.xs[2]-14.0).abs()<0.01,"typed leading spaces retain their advances"); }
        assert!(para.lines.last().unwrap().xs.last().unwrap() < &100.0,"the final line is not expanded");
    }
}

#[test]
fn fixed_pitch_balance_applies_tracking_twice_to_fullwidth_text_and_once_to_latin() {
    if !wordcraft_fonts::FontDb::global().has_family("宋体") { return; }
    for spacing in [-0.2, 0.2] {
        let mut d = Document::from_text("甲乙AB");
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.mark.font=Some("宋体".into()); p.mark.size=Some(12.0);
        for r in &mut p.runs { r.props.font=Some("宋体".into()); r.props.size=Some(12.0); r.props.spacing=Some(spacing); }
        let advances = |d: &Document| {
            let l=lay(d);
            let line=l.pages[0].items.iter().find_map(|it| if let Placed::Lines {para,..}=it {para.lines.first()} else {None}).unwrap();
            (line.xs[1]-line.xs[0],line.xs[3]-line.xs[2])
        };
        let off=advances(&d);
        d.settings.balance_single_byte_double_byte_width=true;
        let on=advances(&d);
        assert!((on.0-off.0-spacing).abs()<0.01,"actual Word fullwidth tracking: {spacing} {off:?} {on:?}");
        assert!((on.1-off.1).abs()<0.01,"Latin tracking is unchanged");
    }
}

#[test]
fn line_grid_snaps_line_heights_and_fixes_lines_per_page() {
    let text = vec!["Grid line"; 80].join("\n");
    let d = with_grid(&text, DocGridType::Lines, 0);
    let (first, hs) = lines(&lay(&d));
    for h in &hs {
        let cells = h / 18.0;
        assert!((cells - cells.round()).abs() < 1e-3 && cells >= 1.0, "line height {h} is not a multiple of the pitch");
    }
    // Text at the default size fits one pitch: 648 pt of text height holds 36 lines.
    let h = hs[0];
    assert_eq!(first, (648.0 / h + 1e-3).floor() as usize, "heights {h}");
    // Turning the paragraphs' snapToGrid off leaves their natural line heights.
    let mut off = d.clone();
    for b in off.body.iter_mut() {
        if let Block::Para(p) = std::sync::Arc::make_mut(b) {
            p.props.snap_to_grid = Some(false);
        }
    }
    let plain = with_grid(&text, DocGridType::Default, 0);
    assert_eq!(lines(&lay(&off)).1[0], lines(&lay(&plain)).1[0]);
}

#[test]
fn character_grid_places_east_asian_text_on_the_pitch() {
    let text = "日本語の文書abc";
    // The default font size plus 2.5 pt: a pitch unlike any font's advance.
    let lines = wordcraft_doc::SectionProps { doc_grid: Some(DocGrid { kind: DocGridType::Lines, ..Default::default() }), ..Default::default() };
    let size = para::Grid::of(&Document::from_text(""), &lines).font_size;
    let cs = DocGrid::char_space_for(size + 2.5, size);
    let d = with_grid(text, DocGridType::LinesAndChars, cs);
    let pitch = d.last_section.grid_char_pitch(size).unwrap();
    assert!((pitch - (size + 2.5)).abs() < 0.01);
    let l = lay(&d);
    let plain = lay(&with_grid(text, DocGridType::Lines, cs));
    let first =
        |l: &DocLayout| l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para.clone()) } else { None }).unwrap();
    let (g, p) = (first(&l), first(&plain));
    let line = &g.lines[0];
    let latin = text.find('a').unwrap();
    for (k, c) in g.clusters.iter().enumerate() {
        let x = line.xs[k] - line.xs[0];
        if c.start < latin {
            // Ideographs and kana sit on the pitch, one cell each.
            assert!((x - pitch * k as f32).abs() < 0.01, "cluster {k} at {x}, pitch {pitch}");
            assert!((c.adv - pitch).abs() < 0.01);
        } else {
            // Latin text keeps its own advances.
            assert!((c.adv - p.clusters[k].adv).abs() < 0.01, "latin cluster {k}");
        }
    }
}

#[test]
fn ordinary_cjk_overflow_uses_the_word_width_sweep_budget() {
    if !wordcraft_fonts::FontDb::global().has_family("宋体") { return; }
    let text = "甲，乙。丙：丁；戊！己？庚辛壬癸子丑寅卯辰巳";
    // Independently generated Word 16.0.20430 controls, 14 pt SimSun,
    // left alignment, no grid, 24 pt exact spacing. The widths bracket
    // the observed change; no text or page-specific constant is used.
    for (width, expected) in [(174.0, "甲，乙。丙：丁；戊！己？"), (178.0, "甲，乙。丙：丁；戊！己？庚"), (188.0, "甲，乙。丙：丁；戊！己？庚"), (190.0, "甲，乙。丙：丁；戊！己？庚辛")] {
        let mut d = Document::from_text(text);
        d.settings.character_spacing_control = wordcraft_doc::CharacterSpacingControl::CompressPunctuation;
        d.last_section.page_w = width + 144.0;
        d.last_section.margin_left = 72.0; d.last_section.margin_right = 72.0;
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.mark.font = Some("宋体".into()); p.mark.size = Some(14.0);
        p.props.line_spacing = Some(LineSpacing::Exactly(24.0));
        for r in &mut p.runs { r.props.font = Some("宋体".into()); r.props.size = Some(14.0); }
        let l = lay(&d);
        let stop = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para.lines[0].stop) } else { None }).unwrap();
        assert_eq!(&text[..stop], expected, "Word control at {width} pt");
    }
}

#[test]
fn ideograph_with_closing_bracket_uses_the_group_budget() {
    if !wordcraft_fonts::FontDb::global().has_family("宋体") { return; }
    // Fixed public application-template paragraph, Word 16.0.20430 page 3.
    // The closing bracket stays with the ideograph; it is not an isolated
    // ordinary-character overflow. The companion width-sweep test protects
    // the smaller budget for an ordinary character with a break after it.
    let text = "一、申请人须逐项如实填写,填表所用代码以当年发布的《国家社会科学基金项目申报数据代码表》为准。";
    let mut d = Document::from_text(text);
    d.settings.character_spacing_control = wordcraft_doc::CharacterSpacingControl::CompressPunctuation;
    d.last_section.page_w = 464.95 + 144.0;
    d.last_section.margin_left = 72.0; d.last_section.margin_right = 72.0;
    let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
    p.mark.font = Some("宋体".into()); p.mark.size = Some(10.5);
    p.props.indent_first = Some(21.0);
    p.props.line_spacing = Some(LineSpacing::Exactly(20.25));
    for r in &mut p.runs { r.props.font = Some("宋体".into()); r.props.size = Some(10.5); }
    let l = lay(&d);
    let stop = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para.lines[0].stop) } else { None }).unwrap();
    assert_eq!(&text[..stop], "一、申请人须逐项如实填写,填表所用代码以当年发布的《国家社会科学基金项目申报数据代码表》");
}

#[test]
fn east_asian_punctuation_kerning_obeys_hint_and_threshold() {
    if !wordcraft_fonts::FontDb::global().has_family("宋体") { return; }
    for (hint, kern, expected_quote, expected_bracket) in [(None, 0.0, 14.0, 14.0), (None, 0.5, 14.0, 7.0), (Some("eastAsia"), 0.5, 7.0, 7.0), (Some("eastAsia"), 50.0, 14.0, 14.0)] {
        let text = "甲”，乙。”丙，“丁》）戊，（己";
        let mut d = Document::from_text(text);
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        for run in &mut p.runs {
            run.props.font = Some("宋体".into()); run.props.size = Some(14.0);
            run.props.kern = Some(kern); run.props.font_hint = hint.map(str::to_string);
        }
        let l = lay(&d);
        let para = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(para) } else { None }).unwrap();
        for (ch, want) in [('”', expected_quote), ('》', expected_bracket)] {
            let pos = text.find(ch).unwrap();
            let c = para.clusters.iter().find(|c| c.start == pos).unwrap();
            assert!((c.adv - want).abs() < 0.02, "{hint:?} {kern} {ch}: {} != {want}", c.adv);
        }
    }
}

#[test]
fn legacy_mode_fits_sentence_end_group_but_modern_mode_wraps() {
    use wordcraft_doc::CharacterSpacingControl;
    if !wordcraft_fonts::FontDb::global().has_family("SimSun") { return; }
    let text = "对课题申请人所在单位意见的审核意见；是否同意报全国哲学社会科学工作办公室；其他意见。";
    for bold in [false, true] {
        for (mode, expected) in [(14, 1), (15, 2)] {
            let mut d = Document::from_text(text);
            d.settings.compat_mode = mode;
            d.settings.character_spacing_control = CharacterSpacingControl::CompressPunctuation;
            d.last_section.page_w = 428.35 + 144.0;
            d.last_section.margin_left = 72.0;
            d.last_section.margin_right = 72.0;
            let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
            p.mark.font = Some("SimSun".into()); p.mark.size = Some(10.5);
            for run in &mut p.runs { run.props.font = Some("SimSun".into()); run.props.size = Some(10.5); run.props.bold = Some(bold); }
            let l = lay(&d);
            let count: usize = l.pages.iter().flat_map(|p| &p.items).filter_map(|i| {
                if let Placed::Lines { para, .. } = i { Some(para.lines.len()) } else { None }
            }).sum();
            assert_eq!(count, expected, "Word 16 mode={mode} bold={bold}");
            if mode == 14 {
                let para = l.pages[0].items.iter().find_map(|i| if let Placed::Lines { para, .. } = i { Some(para) } else { None }).unwrap();
                let xs = &para.lines[0].xs;
                let tail_advance = 5.25 + if bold { 10.5 / 256.0 } else { 0.0 };
                assert!((xs[xs.len() - 1] - xs[xs.len() - 2] - tail_advance).abs() < 0.02,
                    "Word consumes the line-end full stop half-cell, including the final width used by justification");
            }
        }
    }
}

#[test]
fn fixed_pitch_compatibility_spaces_use_half_em_not_the_latin_space_width() {
    if !wordcraft_fonts::FontDb::global().has_family("Times New Roman") { return; }
    for balanced in [false, true] {
        let mut d = Document::from_text("A    B");
        d.settings.balance_single_byte_double_byte_width = balanced;
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        for run in &mut p.runs { run.props.font = Some("Times New Roman".into()); run.props.size = Some(10.5); }
        let l = lay(&d);
        let p = l.pages[0].items.iter().find_map(|i| if let Placed::Lines { para, .. } = i { Some(para) } else { None }).unwrap();
        for c in p.clusters.iter().filter(|c| c.kind == crate::para::ClKind::Space) {
            assert!((c.adv - if balanced { 5.25 } else { 2.625 }).abs() < 0.01);
        }
    }
}

#[test]
fn legacy_justification_expands_automatic_spacing_before_chinese_gaps() {
    if !wordcraft_fonts::FontDb::global().has_family("SimSun") { return; }
    let text = "研究项目填1个字符说明与具体要求研究项目填1个字符说明与具体要求";
    let mut d = Document::from_text(text);
    d.settings.compat_mode = 14;
    d.settings.balance_single_byte_double_byte_width = true;
    d.last_section.page_w = 274.0;
    d.last_section.margin_left = 72.0;
    d.last_section.margin_right = 72.0;
    let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
    p.props.align = Some(wordcraft_doc::props::Align::Justify);
    for run in &mut p.runs { run.props.font = Some("SimSun".into()); run.props.size = Some(10.5); }
    let l = lay(&d);
    let p = l.pages[0].items.iter().find_map(|i| if let Placed::Lines { para, .. } = i { Some(para) } else { None }).unwrap();
    let line = &p.lines[0];
    assert_eq!(&text[..line.stop], "研究项目填1个字符说明与");
    assert!((line.xs[1] - line.xs[0] - 10.5).abs() < 0.02, "Word retains full-em adjacent Chinese advances");
    assert!((line.xs[5] - line.xs[4] - 15.125).abs() < 0.15, "Word adds slack around the number: {:?}", line.xs);
}

#[test]
fn measured_legacy_faux_bold_adds_one_font_unit_to_the_advance() {
    for font in ["SimSun", "FangSong"] {
        if !wordcraft_fonts::FontDb::global().has_family(font) { continue; }
        for size in [10.5, 16.0] {
            for bold in [false, true] {
                let mut d = Document::from_text("甲乙丙丁戊己庚辛壬癸");
                d.settings.compat_mode = 14;
                let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
                for r in &mut p.runs { r.props.font = Some(font.into()); r.props.size = Some(size); r.props.bold = Some(bold); }
                let l = lay(&d);
                let para = l.pages[0].items.iter().find_map(|i| if let Placed::Lines { para, .. } = i { Some(para) } else { None }).unwrap();
                let expected = size + if bold { size / 256.0 } else { 0.0 };
                assert!((para.clusters[0].adv - expected).abs() < 0.001, "{font} {size} {bold}");
            }
        }
    }
}

#[test]
fn balanced_ascii_space_uses_double_tracking_without_changing_latin_tracking() {
    if !wordcraft_fonts::FontDb::global().has_family("Times New Roman") { return; }
    for spacing in [-0.2, 0.0, 0.2] {
        let mut d=Document::from_text("A B");
        d.settings.compat_mode=14;
        d.settings.balance_single_byte_double_byte_width=true;
        let p=d.para_mut(StoryRef::Body,&Path::top(0)).unwrap();
        p.mark.font=Some("Times New Roman".into());p.mark.size=Some(10.5);
        for r in &mut p.runs {r.props.font=Some("Times New Roman".into());r.props.size=Some(10.5);r.props.spacing=Some(spacing);}
        let l=lay(&d);
        let line=l.pages[0].items.iter().find_map(|it| if let Placed::Lines {para,..}=it {para.lines.first()} else {None}).unwrap();
        assert!((line.xs[2]-line.xs[1]-(5.25+2.0*spacing)).abs()<0.001,"Word balance flag space tracking, spacing={spacing}");
    }
}

#[test]
fn legacy_auto_space_uses_tracked_asian_width() {
    if !wordcraft_fonts::FontDb::global().has_family("宋体") {return;}
    for balanced in [false,true] {
        for spacing in [-0.2,0.2] {
            let mut d=Document::from_text("甲A");d.settings.compat_mode=14;d.settings.balance_single_byte_double_byte_width=balanced;
            let p=d.para_mut(StoryRef::Body,&Path::top(0)).unwrap();p.mark.size=Some(12.0);p.mark.font=Some("宋体".into());
            for r in &mut p.runs {r.props.size=Some(12.0);r.props.font=Some("宋体".into());r.props.spacing=Some(spacing);}
            let l=lay(&d);let para=l.pages[0].items.iter().find_map(|it|if let Placed::Lines {para,..}=it {Some(para)}else{None}).unwrap();
            let tracked=12.0+spacing*if balanced {2.0}else{1.0};
            assert!((para.lines[0].xs[1]-tracked*1.25).abs()<0.001,"balance={balanced}, spacing={spacing}");
        }
    }
}

#[test]
fn legacy_faux_bold_hanging_changes_continuation_but_preserves_start() {
    if !wordcraft_fonts::FontDb::global().has_family("SimSun") { return; }
    let mut observed = Vec::new();
    for bold in [false, true] {
        let mut d = Document::from_text("甲乙丙丁戊己庚辛");
        d.settings.compat_mode = 14;
        d.styles.get_mut("Normal").unwrap().chr.size = Some(10.5);
        let p = d.para_mut(StoryRef::Body, &Path::top(0)).unwrap();
        p.props.indent_left_chars = Some(1.82);
        p.props.indent_first_chars = Some(-7.56);
        for run in &mut p.runs {
            run.props.font = Some("SimSun".into());
            run.props.font_ea = Some("SimSun".into());
            run.props.size = Some(12.0);
            run.props.bold = Some(bold);
        }
        let l = lay(&d);
        let rp = l.pages[0].items.iter().find_map(|it| if let Placed::Lines { para, .. } = it { Some(&para.rp) } else { None }).unwrap();
        observed.push((rp.indent_left, rp.indent_left + rp.indent_first));
    }
    assert!((observed[0].1 - observed[1].1).abs() < 0.001, "start must not move when only the hanging run becomes bold");
    assert!(observed[1].0 > observed[0].0 + 0.3, "bold continuation must use the measured fullwidth advance");
    assert!(observed[1].0 < observed[0].0 + 0.4, "do not add a whole screen pixel to every indentation character");
}
