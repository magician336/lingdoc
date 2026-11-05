use serde_json::{Value, json};
use wordcraft_doc::{Document, Pos};
use wordcraft_engine::Session;

fn request(s: &Session, block: usize, start: usize, end: usize, old: &str, new: &str) -> Value {
    json!({"documentId":s.document_id(),"revision":s.rev(),"anchor":Pos::body(block,start),
        "focus":Pos::body(block,end),"expectedText":old,"text":new})
}

#[test]
fn exact_unicode_replace_ignores_typing_preferences_and_is_one_undo_step() {
    let mut s = Session::new(Document::from_text("保留😀样本120名结束\n相同样本120名不变"));
    s.prefs.typing_replaces_selection = false;
    s.prefs.overtype = true;
    let before = s.doc.clone();
    let start = "保留😀".len();
    let r = request(&s, 0, start, start + "样本120名".len(), "样本120名", "毕业年级样本120名");
    let mark = s.undo_mark();
    s.run("text.replaceRange", &r).unwrap();
    assert_eq!(s.doc.para_at(&Pos::body(0, 0)).unwrap().text, "保留😀毕业年级样本120名结束");
    assert_eq!(s.doc.para_at(&Pos::body(1, 0)), before.para_at(&Pos::body(1, 0)));
    assert_eq!(s.undo_mark(), mark + 1);
    s.run("edit.undo", &json!({})).unwrap();
    assert_eq!(s.doc, before);
    s.run("edit.redo", &json!({})).unwrap();
    assert!(s.doc.para_at(&Pos::body(0, 0)).unwrap().text.contains("毕业年级"));
}

#[test]
fn stale_text_revision_document_and_invalid_ranges_leave_document_unchanged() {
    let mut s = Session::new(Document::from_text("😀样本\n😀样本"));
    let r = request(&s, 0, 0, 4, "😀", "甲");
    let before = s.doc.clone();
    let revision = s.rev();
    let mark = s.undo_mark();
    for (key, value) in [
        ("documentId", json!(s.document_id().saturating_add(1))),
        ("revision", json!(revision.saturating_add(1))),
        ("expectedText", json!("错误")),
        ("focus", json!(Pos::body(0, 2))),
        ("focus", json!(Pos::body(0, 9999))),
        ("focus", json!(Pos::body(1, 4))),
        ("text", json!("甲\r\n乙")),
        ("text", json!("甲\u{fffc}乙")),
    ] {
        let mut bad = r.clone();
        bad[key] = value;
        assert!(s.run("text.replaceRange", &bad).is_err(), "{bad}");
        assert_eq!(s.doc, before);
        assert_eq!(s.rev(), revision);
        assert_eq!(s.undo_mark(), mark);
    }
}

#[test]
fn refuses_track_changes_and_protection_without_toggling_them() {
    let mut s = Session::new(Document::from_text("样本"));
    s.doc.settings.track_changes = true;
    let r = request(&s, 0, 0, 6, "样本", "限定样本");
    let before = s.doc.clone();
    assert!(s.run("text.replaceRange", &r).is_err());
    assert_eq!(s.doc, before);
    s.doc.settings.track_changes = false;
    s.doc.settings.protection = Some("readOnly".into());
    let before = s.doc.clone();
    assert!(s.run("text.replaceRange", &r).is_err());
    assert_eq!(s.doc, before);
}

#[test]
fn repeated_text_targets_only_exact_paragraph_and_user_edit_stales_request() {
    let mut s = Session::new(Document::from_text("相同句子\n相同句子"));
    let r = request(&s, 1, 0, "相同句子".len(), "相同句子", "第二段修改");
    s.run("text.replaceRange", &r).unwrap();
    assert_eq!(s.doc.para_at(&Pos::body(0, 0)).unwrap().text, "相同句子");
    assert_eq!(s.doc.para_at(&Pos::body(1, 0)).unwrap().text, "第二段修改");
    assert!(s.run("text.replaceRange", &r).is_err());
    let r = request(&s, 0, 0, "相同句子".len(), "相同句子", "不应应用");
    s.run("text.insert", &json!({"text":"用户续写"})).unwrap();
    let before = s.doc.clone();
    assert!(s.run("text.replaceRange", &r).is_err());
    assert_eq!(s.doc, before);
}

#[test]
fn replacement_preserves_neighbor_formatting_and_docx_roundtrip() {
    let mut s = Session::new(Document::from_text("前缀样本后缀\n保留段落"));
    s.run("select.range", &json!({"anchor":Pos::body(1,0),"focus":Pos::body(1,"保留段落".len())})).unwrap();
    s.run("format.bold", &json!({})).unwrap();
    let neighbor = s.doc.para_at(&Pos::body(1, 0)).unwrap().clone();
    let r = request(&s, 0, "前缀".len(), "前缀样本".len(), "样本", "限定样本");
    s.run("text.replaceRange", &r).unwrap();
    assert_eq!(s.doc.para_at(&Pos::body(1, 0)), Some(&neighbor));
    let data = wordcraft_engine::io::save_bytes("controlled.docx", &s.doc).unwrap();
    assert!(!data.is_empty());
    let reopened = wordcraft_engine::io::open_bytes("controlled.docx", &data).unwrap();
    assert_eq!(reopened.para_at(&Pos::body(0, 0)).unwrap().text, "前缀限定样本后缀");
    assert_eq!(reopened.para_at(&Pos::body(1, 0)).unwrap().text, "保留段落");
    assert_eq!(reopened.para_at(&Pos::body(1, 0)).unwrap().props_of_char(0).bold, Some(true));
}

#[test]
fn single_table_cell_paragraph_is_supported_without_touching_other_cells() {
    let mut s = Session::new(Document::new());
    s.run("insert.table", &json!({"rows":2,"cols":2})).unwrap();
    s.run("text.insert", &json!({"text":"样本120名"})).unwrap();
    let mut anchor = s.sel.focus.clone();
    anchor.off = 0;
    let focus = s.sel.focus.clone();
    s.run("text.tab", &json!({})).unwrap();
    s.run("text.insert", &json!({"text":"邻格保留"})).unwrap();
    let neighbor_pos = s.sel.focus.clone();
    let neighbor = s.doc.para_at(&neighbor_pos).unwrap().clone();
    let before = s.doc.clone();
    let r = json!({"documentId":s.document_id(),"revision":s.rev(),"anchor":anchor,"focus":focus,
        "expectedText":"样本120名","text":"毕业年级样本120名"});
    s.run("text.replaceRange", &r).unwrap();
    assert_eq!(s.doc.para_at(&anchor).unwrap().text, "毕业年级样本120名");
    assert_eq!(s.doc.para_at(&neighbor_pos), Some(&neighbor));
    s.run("edit.undo", &json!({})).unwrap();
    assert_eq!(s.doc, before);
}

#[test]
fn pending_ime_undo_join_cannot_merge_reviewed_replacement_with_user_typing() {
    let mut s = Session::new(Document::from_text("样本"));
    s.run("text.insert", &json!({"text":"用户输入"})).unwrap();
    let before = s.doc.clone();
    let mark = s.undo_mark();
    let r = request(&s, 0, "用户输入".len(), "用户输入样本".len(), "样本", "限定样本");
    s.join_next_undo();
    s.run("text.replaceRange", &r).unwrap();
    assert_eq!(s.undo_mark(), mark + 1);
    s.run("edit.undo", &json!({})).unwrap();
    assert_eq!(s.doc, before);
}

#[test]
fn literal_quote_is_not_rewritten_by_autocorrect() {
    let mut s = Session::new(Document::from_text("x后缀"));
    s.prefs.autocorrect.enabled = true;
    s.prefs.autocorrect.smart_quotes = true;
    let r = request(&s, 0, 0, 1, "x", "\"");
    s.run("text.replaceRange", &r).unwrap();
    assert_eq!(s.doc.para_at(&Pos::body(0, 0)).unwrap().text, "\"后缀");
}

#[test]
fn existing_comment_markers_are_refused_without_losing_comment() {
    let mut s = Session::new(Document::from_text("样本说明"));
    s.run("select.range", &json!({"anchor":Pos::body(0,0),"focus":Pos::body(0,"样本".len())})).unwrap();
    s.run("review.newComment", &json!({"text":"保留老师意见"})).unwrap();
    let before = s.doc.clone();
    let r = request(&s, 0, 0, "样本".len(), "样本", "限定样本");
    assert!(s.run("text.replaceRange", &r).is_err());
    assert_eq!(s.doc, before);
}

#[test]
fn same_paragraph_prefix_bold_and_suffix_italic_remain_unchanged() {
    let mut s = Session::new(Document::from_text("前缀样本后缀"));
    s.run("select.range", &json!({"anchor":Pos::body(0,0),"focus":Pos::body(0,"前缀".len())})).unwrap();
    s.run("format.bold", &json!({})).unwrap();
    s.run("select.range", &json!({"anchor":Pos::body(0,"前缀样本".len()),"focus":Pos::body(0,"前缀样本后缀".len())})).unwrap();
    s.run("format.italic", &json!({})).unwrap();
    let paragraph = s.doc.para_at(&Pos::body(0, 0)).unwrap();
    let prefix_props = paragraph.props_of_char(0).clone();
    let target_props = paragraph.props_of_char("前缀".len()).clone();
    let suffix_props = paragraph.props_of_char("前缀样本".len()).clone();
    assert_eq!(prefix_props.bold, Some(true));
    assert_eq!(suffix_props.italic, Some(true));
    assert_ne!(target_props.bold, Some(true));
    assert_ne!(target_props.italic, Some(true));
    let r = request(&s, 0, "前缀".len(), "前缀样本".len(), "样本", "限定样本");
    s.run("text.replaceRange", &r).unwrap();
    let paragraph = s.doc.para_at(&Pos::body(0, 0)).unwrap();
    assert_eq!(paragraph.text, "前缀限定样本后缀");
    for offset in [0, "前".len()] {
        assert_eq!(paragraph.props_of_char(offset), &prefix_props);
    }
    for offset in ["前缀限定样本".len(), "前缀限定样本后".len()] {
        assert_eq!(paragraph.props_of_char(offset), &suffix_props);
    }
    assert_eq!(paragraph.props_of_char("前缀".len()), &target_props);
}
