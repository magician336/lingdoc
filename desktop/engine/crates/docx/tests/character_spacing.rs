//! Hand-written OOXML: compression settings must survive a real package read/write.
use std::io::{Read, Write};
use wordcraft_doc::CharacterSpacingControl;

fn package(control: &str, theme: Option<&str>) -> Vec<u8> {
    let rel="http://schemas.openxmlformats.org/officeDocument/2006/relationships";
    let mut zip=zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
    let root=format!(r#"<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="d" Type="{rel}/officeDocument" Target="word/document.xml"/></Relationships>"#);
    let document=r#"<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>甲，乙。丙</w:t></w:r></w:p></w:body></w:document>"#;
    let settings=format!(r#"<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">{control}</w:settings>"#);
    let mut relationships=format!(r#"<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="s" Type="{rel}/settings" Target="settings.xml"/>"#);
    if theme.is_some(){relationships.push_str(&format!(r#"<Relationship Id="t" Type="{rel}/theme" Target="theme/theme1.xml"/>"#));}
    relationships.push_str("</Relationships>");
    let mut parts=vec![("_rels/.rels",root.as_str()),("word/document.xml",document),("word/_rels/document.xml.rels",relationships.as_str()),("word/settings.xml",settings.as_str())];
    if let Some(t)=theme{parts.push(("word/theme/theme1.xml",t));}
    for (name,bytes) in parts{zip.start_file(name,zip::write::SimpleFileOptions::default()).unwrap();zip.write_all(bytes.as_bytes()).unwrap();}
    zip.finish().unwrap().into_inner()
}

fn part(bytes: &[u8],name: &str)->String {
    let mut zip=zip::ZipArchive::new(std::io::Cursor::new(bytes)).unwrap();
    let mut text=String::new();zip.by_name(name).unwrap().read_to_string(&mut text).unwrap();text
}

#[test]
fn character_spacing_all_modes_survive_and_can_change() {
    for mode in [CharacterSpacingControl::DoNotCompress,CharacterSpacingControl::CompressPunctuation,CharacterSpacingControl::CompressPunctuationAndJapaneseKana] {
        let input=package(&format!(r#"<w:characterSpacingControl w:val="{}"/>"#,mode.ooxml()),None);
        let mut doc=wordcraft_docx::read(&input).unwrap();assert_eq!(doc.settings.character_spacing_control,mode);
        let bytes=wordcraft_docx::write(&doc).unwrap();assert_eq!(wordcraft_docx::read(&bytes).unwrap().settings.character_spacing_control,mode);
        doc.settings.character_spacing_control=CharacterSpacingControl::DoNotCompress;
        assert!(part(&wordcraft_docx::write(&doc).unwrap(),"word/settings.xml").contains(r#"w:characterSpacingControl w:val="doNotCompress""#));
    }
    for omitted_or_invalid in ["",r#"<w:characterSpacingControl w:val="hostile"/>"#]{
        assert_eq!(wordcraft_docx::read(&package(omitted_or_invalid,None)).unwrap().settings.character_spacing_control,CharacterSpacingControl::DoNotCompress);
    }
}

#[test]
fn ordinary_edits_keep_full_script_theme_and_explicit_theme_change_is_not_stale() {
    let theme=r#"<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Handwritten"><a:themeElements><a:fontScheme name="Handwritten"><a:majorFont><a:latin typeface="Heading"/><a:ea typeface="CJK Heading"/><a:cs typeface="Arabic"/><a:font script="Hans" typeface="CJK Script"/></a:majorFont><a:minorFont><a:latin typeface="Body"/><a:ea typeface="CJK Body"/><a:cs typeface=""/><a:font script="Jpan" typeface="Japanese"/></a:minorFont></a:fontScheme><a:fmtScheme name="Keep This"/></a:themeElements><a:objectDefaults/></a:theme>"#;
    let mut doc=wordcraft_docx::read(&package("",Some(theme))).unwrap();
    // An actual body edit must not discard script font mappings or unrelated theme markup.
    doc.body=wordcraft_doc::Document::from_text("Changed body").body;
    assert_eq!(part(&wordcraft_docx::write(&doc).unwrap(),"word/theme/theme1.xml"),theme);
    doc.settings.major_font="New Heading".into();
    let output=part(&wordcraft_docx::write(&doc).unwrap(),"word/theme/theme1.xml");
    assert!(output.contains("New Heading"));assert_ne!(output,theme);
}

#[test]
fn east_asian_hint_does_not_rewrite_inherited_latin_digits() {
    let bytes=package("",None);
    let mut parts=zip::ZipArchive::new(std::io::Cursor::new(bytes)).unwrap();
    let mut original=String::new();parts.by_name("word/document.xml").unwrap().read_to_string(&mut original).unwrap();
    let body=original.replace("<w:r><w:t>",r#"<w:r><w:rPr><w:rFonts w:eastAsia="CJK Face" w:hint="eastAsia"/></w:rPr><w:t>"#);
    let mut writer=zip::ZipWriter::new(std::io::Cursor::new(Vec::new()));
    for i in 0..parts.len(){let mut f=parts.by_index(i).unwrap();let mut b=Vec::new();f.read_to_end(&mut b).unwrap();writer.start_file(f.name(),zip::write::SimpleFileOptions::default()).unwrap();writer.write_all(if f.name()=="word/document.xml"{body.as_bytes()}else{&b}).unwrap();}
    let doc=wordcraft_docx::read(&writer.finish().unwrap().into_inner()).unwrap();
    let c=&doc.body[0].as_para().unwrap().runs[0].props;
    assert_eq!(c.font,None);assert_eq!(c.font_ea.as_deref(),Some("CJK Face"));assert_eq!(c.font_hint.as_deref(),Some("eastAsia"));
    let saved=part(&wordcraft_docx::write(&doc).unwrap(),"word/document.xml");
    assert!(saved.contains(r#"w:eastAsia="CJK Face" w:hint="eastAsia""#));
    assert!(!saved.contains(r#"w:ascii="CJK Face""#));
}

#[test]
fn balance_tracking_switch_changes_without_dropping_other_compatibility_flags() {
    let control=r#"<w:compat><w:balanceSingleByteDoubleByteWidth/><w:doNotExpandShiftReturn/><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat>"#;
    let mut doc=wordcraft_docx::read(&package(control,None)).unwrap();
    assert!(doc.settings.balance_single_byte_double_byte_width);
    assert!(wordcraft_docx::read(&wordcraft_docx::write(&doc).unwrap()).unwrap().settings.balance_single_byte_double_byte_width);
    doc.settings.balance_single_byte_double_byte_width=false;
    let bytes=wordcraft_docx::write(&doc).unwrap();let settings=part(&bytes,"word/settings.xml");
    assert!(!settings.contains("w:balanceSingleByteDoubleByteWidth"));assert!(settings.contains("w:doNotExpandShiftReturn"));
    let mut again=wordcraft_docx::read(&bytes).unwrap();assert!(!again.settings.balance_single_byte_double_byte_width);
    again.settings.balance_single_byte_double_byte_width=true;
    let settings=part(&wordcraft_docx::write(&again).unwrap(),"word/settings.xml");
    assert_eq!(settings.matches("<w:balanceSingleByteDoubleByteWidth").count(),1);assert!(settings.contains("w:doNotExpandShiftReturn"));
}
