use serde_json::json;
use crate::Session;
use wordcraft_doc::{Document, StoryRef};

fn inserted_objects() -> Vec<(&'static str, serde_json::Value)> {
    let png = "iVBORw0KGgoAAAANSUhEUgAAACgAAAAUCAYAAAD/Rn+7AAAAMUlEQVR4nO3OMREAMBDDMPMnnaL4cwcP2sVgP8MOFCxoBwoWtAMFC9qBggXtQMEdBx+Jezobd7ILdgAAABZ0RVh0TGluZ0RvY0RyYXdpbmcAZml4dHVyZXWgp10AAAAASUVORK5CYII=";
    vec![
        ("insert.chart", json!({"type":"column"})),
        ("insert.smartArt", json!({"layout":"basicList"})),
        ("insert.shape", json!({"kind":"rectangle"})),
        ("insert.picture", json!({"data":png})),
        ("insert.textBox", json!({"text":"Box"})),
        ("insert.equation", json!({"linear":"x^2"})),
        ("insert.canvas", json!({})),
        ("insert.field", json!({"instr":"DATE", "result":"Date"})),
        ("insert.dateTime", json!({"update":true})),
        ("insert.pageNumber", json!({"position":"current"})),
    ]
}

#[test]
fn cover_page_keeps_date_on_one_page_and_preserves_body() {
    for (w, h) in [(612.0, 792.0), (595.28, 841.89), (792.0, 612.0)] {
        let mut doc = Document::from_text("Original body");
        doc.last_section.page_w = w;
        doc.last_section.page_h = h;
        let mut s = Session::new(doc);
        s.run("insert.coverPage", &json!({})).unwrap();
        assert_eq!(s.export_layout().pages.len(), 2, "paper {w} x {h}");
        assert!(s.doc.plain_text(StoryRef::Body).contains("Original body"));
        let reopened = Session::new(wordcraft_docx::read(&wordcraft_docx::write(&s.doc).unwrap()).unwrap());
        assert_eq!(reopened.export_layout().pages.len(), 2);
    }
}

#[test]
fn blank_page_typing_targets_the_new_blank_page() {
    for off in [0, 6, 12] {
        let mut s = Session::new(Document::from_text("Before After"));
        let at = wordcraft_doc::Pos { off, ..s.doc.start_of(StoryRef::Body) };
        s.sel = crate::Selection::caret(at);
        s.run("insert.blankPage", &json!({})).unwrap();
        s.run("text.insert", &json!({"text":"New blank page"})).unwrap();
        let text = s.doc.plain_text(StoryRef::Body);
        let segments: Vec<_> = text.split('\u{000C}').collect();
        assert!(segments[usize::from(off != 0)].contains("New blank page"), "offset {off}: {text:?}");
    }
}

#[test]
fn blank_page_at_document_edges_adds_only_one_page() {
    for (off, pages) in [(0, 2), (6, 3), (12, 2)] {
        let mut s = Session::new(Document::from_text("Before After"));
        let at = wordcraft_doc::Pos { off, ..s.doc.start_of(StoryRef::Body) };
        s.sel = crate::Selection::caret(at);
        s.run("insert.blankPage", &json!({})).unwrap();
        assert_eq!(s.export_layout().pages.len(), pages, "offset {off}");
        let reopened = Session::new(wordcraft_docx::read(&wordcraft_docx::write(&s.doc).unwrap()).unwrap());
        assert_eq!(reopened.export_layout().pages.len(), pages);
        assert!(reopened.doc.plain_text(StoryRef::Body).contains("Before"));
        assert!(reopened.doc.plain_text(StoryRef::Body).contains("After"));
    }
}

#[test]
fn thesaurus_uses_selected_word_with_trailing_space() {
    let mut s = Session::new(Document::from_text("Good document"));
    let a = s.doc.start_of(StoryRef::Body);
    let b = wordcraft_doc::Pos { off: 5, ..a.clone() };
    s.sel = crate::Selection { anchor: a, focus: b };
    let result = s.run("review.thesaurus", &json!({})).unwrap();
    assert_eq!(result["word"], "good");
    assert!(result["synonyms"].as_array().unwrap().iter().any(|v| v == "excellent"));
}

#[test]
fn empty_hyperlink_label_uses_url() {
    let mut s = Session::new(Document::new());
    s.run("insert.link", &json!({"url":"https://example.org/word-test", "text":""})).unwrap();
    assert_eq!(s.doc.plain_text(StoryRef::Body).trim(), "https://example.org/word-test");
    let p = s.doc.para_at(&s.doc.start_of(StoryRef::Body)).unwrap();
    assert_eq!(p.runs[0].props.link.as_deref(), Some("https://example.org/word-test"));
}

#[test]
fn objects_inserted_after_tracking_is_off_survive_reject_all() {
    for (id, params) in inserted_objects() {
        let mut s = Session::new(Document::new());
        s.run("review.trackChanges", &json!({"value":true})).unwrap();
        s.run("text.insert", &json!({"text":"tracked"})).unwrap();
        s.run("review.trackChanges", &json!({"value":false})).unwrap();
        s.run(id, &params).unwrap();
        s.run("review.rejectAll", &json!({})).unwrap();
        let text = s.doc.plain_text(StoryRef::Body);
        assert!(!text.contains("tracked"));
        let p = s.doc.para_at(&s.doc.start_of(StoryRef::Body)).unwrap();
        assert_eq!(p.object_offsets().len(), 1, "{id} must survive rejecting earlier text");
        assert!(p.runs.iter().all(|r| r.props.ins.is_none() && r.props.del.is_none()));
    }
}

#[test]
fn newly_tracked_objects_are_rejected() {
    for (id, params) in inserted_objects() {
        let mut s = Session::new(Document::new());
        s.run("review.trackChanges", &json!({"value":true})).unwrap();
        s.run(id, &params).unwrap();
        s.run("review.rejectAll", &json!({})).unwrap();
        assert!(s.doc.para_at(&s.doc.start_of(StoryRef::Body)).unwrap().object_offsets().is_empty(), "{id}");
    }
}
