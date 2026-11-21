//! Isolated, read-only geometry diagnostic. Not an application entry point.
use wordcraft_engine::{Session, io, layout::Placed};
use sha2::{Digest, Sha256};

fn main() -> Result<(), String> {
    let filename = std::env::args().nth(1).ok_or("input path required")?;
    let doc = io::open_path(std::path::Path::new(&filename))?;
    let mut session = Session::new(doc.clone());
    let export = std::env::args().nth(2).as_deref() != Some("--canvas");
    let layout = if export { session.export_layout() } else { session.layout() };
    let mut rows = Vec::new();
    let mut face_hashes = std::collections::HashMap::new();
    for (page, p) in layout.pages.iter().enumerate() {
        for (area, items) in [("body", &p.items), ("header", &p.header), ("footer", &p.footer)] {
        for item in items {
            if let Placed::Lines { story, path, para, l0, l1, x, y, .. } = item {
                let text = doc.para(*story, path).map(|p| p.text.as_str()).unwrap_or("");
                rows.push(serde_json::json!({"page":page+1,"area":area,"story":format!("{story:?}"),"layoutMode":if export {"exportPrint"} else {"canvasSession"},"path":path.0,"x":x,"y":y,"text":text,
                    "lineSpacing":format!("{:?}", para.rp.line_spacing),
                    "align":format!("{:?}",para.rp.align),
                    "indentLeft":para.rp.indent_left,"indentRight":para.rp.indent_right,"indentFirst":para.rp.indent_first,
                    "styles":para.styles.iter().map(|s| {
                        let digest = face_hashes.entry(s.face.id()).or_insert_with(||
                            Sha256::digest(s.face.get().data()).iter().map(|b| format!("{b:02x}")).collect::<String>());
                        serde_json::json!({
                        "font":s.rc.font,"fontEa":s.rc.font_ea,"size":s.size,"ascent":s.ascent,"descent":s.descent,
                        "face":s.face.get().family,"faceId":format!("{:?}",s.face.id()),
                        "faceStyle":s.face.get().style,"faceIndex":s.face.get().index(),
                        "fontBytes":s.face.get().data().len(),"upem":s.face.upem,
                        "faceSha256":digest,
                        "spacing":s.rc.spacing,"scale":s.rc.scale,
                        "bold":s.rc.bold,"italic":s.rc.italic,"synthBold":s.synth_bold,"synthItalic":s.synth_italic
                    })}).collect::<Vec<_>>(),
                    "lines":para.lines[*l0..*l1].iter().map(|l| serde_json::json!({
                        "top":l.top,"height":l.height,"baseline":l.baseline,
                        "left":l.left,"right":l.right,"start":l.start,"stop":l.stop,
                        "text":text.get(l.start..l.stop).unwrap_or(""),"xs":l.xs,
                        "renderedText":para.clusters[l.c0..l.c1].iter().enumerate().map(|(i,c)| {
                            para.shown.iter().find(|(k,_,_)| *k==l.c0+i).map(|(_,s,_)|s.as_str())
                                .unwrap_or_else(||text.get(c.start..c.end).unwrap_or(""))
                        }).collect::<String>(),
                        "clusters":para.clusters[l.c0..l.c1].iter().map(|c| serde_json::json!({
                            "text":text.get(c.start..c.end).unwrap_or(""),"adv":c.adv,"style":c.style,
                            "kind":format!("{:?}",c.kind),"breakAfter":c.break_after
                        })).collect::<Vec<_>>()
                    })).collect::<Vec<_>>()
                }));
            } else if let Placed::Cell { rect, covered, table, row, cell, story, clip } = item {
                rows.push(serde_json::json!({
                    "page":page+1,"area":area,"kind":"cell","table":table.0,"row":row,"cell":cell,
                    "story":format!("{story:?}"),"covered":covered,"clip":clip,
                    "layoutMode":if export {"exportPrint"} else {"canvasSession"},
                    "rect":{"x":rect.x,"y":rect.y,"w":rect.w,"h":rect.h}
                }));
            }
        }
        }
    }
    println!("{}", serde_json::to_string_pretty(&rows).map_err(|e| e.to_string())?);
    Ok(())
}
