//! One metadata catalog for All Tools and command search. Actions remain in the existing UI.

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CommandItem {
    pub id: &'static str,
    pub label: &'static str,
    pub location: &'static str,
    pub category: String,
}

pub const UI_ONLY: [(&str, &str, &str); 5] = [
    ("ui.backstage", "File", "File"),
    ("ui.changePicture", "Change Picture", "Picture Format › Adjust"),
    ("ui.crop", "Crop", "Picture Format › Size"),
    ("ui.linkTextBox", "Link Text Box", "Shape Format › Text"),
    ("ui.discord", "Community Discord", "Help"),
];

pub fn category(id: &str, location: &str) -> String {
    if matches!(id, "view.navigationPane" | "tools.search" | "view.commentsPane" | "view.printLayout" | "ui.backstage") {
        return "LingDoc".into();
    }
    match location.split(" › ").next().unwrap_or(location) {
        "Outlining" => "View".into(),
        "" | "Agents" | "Editing" | "Header & Footer" | "Navigation" | "Quick Access Toolbar" | "Reading" | "Title Bar" => "其他工具".into(),
        top => top.into(),
    }
}

pub fn catalog(session: &wordcraft_engine::Session) -> Vec<CommandItem> {
    let mut seen = std::collections::HashSet::new();
    session.registry.all().iter().map(|s| (s.id, s.label, s.location)).chain(UI_ONLY).filter_map(|(id, label, location)| {
        seen.insert(id).then(|| CommandItem { id, label, location, category: category(id, location) })
    }).collect()
}

pub fn categories(items: &[CommandItem]) -> Vec<String> {
    let mut seen = std::collections::HashSet::new();
    wordcraft_engine::ribbon::TABS.into_iter()
        .chain(wordcraft_engine::ribbon::CONTEXTUAL_TABS)
        .chain(["LingDoc", "其他工具"])
        .chain(items.iter().map(|i| i.category.as_str()))
        .filter(|c| items.iter().any(|i| i.category == *c) && seen.insert((*c).to_string()))
        .map(str::to_string).collect()
}

/// Original command-search matching, including language-aware Turkish casing.
pub fn command_matches(c: &CommandItem, query: &str) -> bool {
    if query.trim().is_empty() { return false; }
    let hay = format!("{} {} {} {} {}", c.label.to_lowercase(), c.location.to_lowercase(), c.id.to_lowercase(),
        crate::i18n::lowercase(tl!(c.label)), crate::i18n::lowercase(&crate::i18n::location(c.location)));
    query.split_whitespace().all(|w| hay.contains(&w.to_lowercase()) || hay.contains(&crate::i18n::lowercase(w)))
}

impl From<&wordcraft_engine::CommandSpec> for CommandItem {
    fn from(c: &wordcraft_engine::CommandSpec) -> Self {
        Self { id: c.id, label: c.label, location: c.location, category: category(c.id, c.location) }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn app() -> crate::WordApp {
        crate::WordApp::new(wordcraft_engine::Session::new(wordcraft_doc::Document::new()), crate::Services::default())
    }

    #[test]
    fn complete_catalog_classifies_every_registered_and_ui_only_command_once() {
        let app = app();
        let items = catalog(&app.session);
        let cats = categories(&items);
        for id in app.session.registry.all().iter().map(|s| s.id).chain(UI_ONLY.iter().map(|s| s.0)) {
            let found: Vec<_> = items.iter().filter(|i| i.id == id).collect();
            assert_eq!(found.len(), 1, "{id}");
            assert!(cats.contains(&found[0].category), "{id}");
        }
        for name in ["References", "Zotero", "LingDoc", "其他工具"] {
            assert!(cats.iter().any(|c| c == name), "{name}");
        }
        assert!(cats.iter().all(|c| items.iter().any(|i| &i.category == c)));
        for id in ["ui.backstage", "view.navigationPane", "tools.search", "view.commentsPane", "view.printLayout"] {
            assert_eq!(items.iter().find(|i| i.id == id).unwrap().category, "LingDoc");
        }
    }

    #[test]
    fn search_independently_covers_the_same_complete_catalog() {
        let app = app();
        // Check registry IDs independently so an empty catalog cannot pass vacuously.
        let items = catalog(&app.session);
        assert!(!items.is_empty());
        for spec in app.session.registry.all() {
            assert!(items.iter().any(|i| i.id == spec.id && command_matches(i, spec.id)), "{}", spec.id);
        }
        for item in &items { assert!(command_matches(item, item.id)); }
        let table = CommandItem { id: "insert.table", label: "Table", location: "Insert › Tables", category: "Insert".into() };
        assert!(command_matches(&table, "  Table   INSERT "));
        assert!(!command_matches(&table, "insert chart"));
        assert!(!command_matches(&table, "   "));
        let cjk = CommandItem { id: "ui.example", label: "章节导航", location: "LingDoc", category: "LingDoc".into() };
        assert!(command_matches(&cjk, "导航 章节"));
    }
}
