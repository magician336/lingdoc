use super::*;

#[derive(Clone)]
pub(super) struct ReaderGroup {
    pub id: u64,
    pub tabs: Vec<String>,
    pub active: String,
    pub weight: f32,
}

impl SourceWorkspace {
    pub(super) fn key(&self) -> String { json!([self.id, self.version]).to_string() }

    pub(super) fn ensure_groups(&mut self) {
        if self.groups.is_empty() {
            self.groups.push(ReaderGroup { id: 1, tabs: vec!["$body".into()], active: "$body".into(), weight: 1.0 });
            self.focus_group = 1;
        }
        self.next_group = self.next_group.max(self.groups.iter().map(|g| g.id).max().unwrap_or(1).saturating_add(1));
    }

    // Allocate space only when creating a group. Opening another tab or restoring a
    // layout must never replace widths chosen by the user.
    fn allocate_group_weight(&mut self, from: usize, key: &str) -> f32 {
        let Some(origin) = self.groups.get(from) else { return 1.0; };
        if origin.tabs.len() == 1 { return origin.weight; }
        let contains_body = origin.tabs.iter().any(|tab| tab == "$body");
        // A third reading group shares existing reading space, leaving the body
        // (including its manually resized width) untouched.
        let donor = if contains_body && key != "$body" && self.groups.len() > 1 {
            self.groups.iter().position(|group| !group.tabs.iter().any(|tab| tab == "$body")).unwrap_or(from)
        } else { from };
        let fraction = if donor == from && contains_body {
            if key == "$body" { 0.62 } else { 0.38 }
        } else { 0.5 };
        let Some(group) = self.groups.get_mut(donor) else { return 1.0; };
        let new_weight = group.weight * fraction;
        let remaining = group.weight - new_weight;
        // Extremely small restored weights must remain valid after splitting.
        if new_weight > 0.0 && remaining > 0.0 {
            group.weight = remaining;
            new_weight
        } else { group.weight }
    }

    pub(super) fn swap_reading_fields(&mut self, other: &mut Self) {
        macro_rules! swap { ($($field:ident),*) => { $(std::mem::swap(&mut self.$field, &mut other.$field);)* }; }
        swap!(id, title, version, pages, text, texture, page, zoom, positions, markdown_raw);
    }

    pub(super) fn remember_current_before_load(&mut self, id: &str, version: &str) {
        self.ensure_groups();
        let next = json!([id, version]).to_string();
        if self.has_source() && self.key() == next { return; }
        if self.has_source() {
            let key = self.key();
            let mut saved = Box::<Self>::default();
            self.swap_reading_fields(&mut saved);
            self.readers.insert(key, saved);
        }
        if let Some(mut saved) = self.readers.remove(&next) { self.swap_reading_fields(&mut saved); }
    }

    pub(super) fn register_current(&mut self) {
        self.ensure_groups();
        if !self.has_source() { return; }
        let key = self.key();
        self.closed_tabs.retain(|(closed,_,_)| closed != &key);
        if let Some(group) = self.groups.iter_mut().find(|g| g.tabs.contains(&key)) {
            group.active = key;
            self.focus_group = group.id;
        } else {
            let index = self.groups.iter().position(|g| g.id == self.focus_group).unwrap_or(0);
            self.groups[index].tabs.push(key.clone());
            self.groups[index].active = key;
            self.focus_group = self.groups[index].id;
        }
    }

    fn known_key(&self, key: &str) -> bool { key == "$body" || (self.has_source() && self.key() == key) || self.readers.contains_key(key) }

    pub(super) fn activate_key(&mut self, key: &str) -> Result<(), String> {
        if !self.known_key(key) { return Err("unknown reader key".into()); }
        if key == "$body" { return Ok(()); }
        if self.has_source() && self.key() == key { return Ok(()); }
        let mut next = self.readers.remove(key).ok_or("reader is not loaded")?;
        if self.has_source() {
            let old = self.key();
            self.swap_reading_fields(&mut next);
            self.readers.insert(old, next);
        } else { self.swap_reading_fields(&mut next); }
        Ok(())
    }

    pub(super) fn set_view_mode(&mut self, mode: &str) -> Result<(), String> {
        if !matches!(mode,"body"|"source"|"split") {return Err("mode must be body, source or split".into());}
        if mode!="body"&&!self.has_source(){return Err("no source has been loaded".into());}
        self.ensure_groups();
        if !self.groups.iter().any(|g|g.tabs.iter().any(|k|k=="$body")) {
            self.groups[0].tabs.insert(0,"$body".into());
        }
        if mode=="body" {
            let g=self.groups.iter_mut().find(|g|g.tabs.iter().any(|k|k=="$body")).ok_or("body group missing")?;
            g.active="$body".into();self.focus_group=g.id;self.maximized=Some(g.id);
        } else {
            self.register_current();let key=self.key();
            if mode=="source" {
                self.maximized=Some(self.focus_group);
            } else {
                let body=self.groups.iter().position(|g|g.tabs.iter().any(|k|k=="$body")).ok_or("body group missing")?;
                let source=self.groups.iter().position(|g|g.tabs.contains(&key)).ok_or("source group missing")?;
                if body==source {
                    // Reuse an existing group when three groups already exist.
                    let target=if self.groups.len()<3 {
                        let id=self.next_group;self.next_group=self.next_group.checked_add(1).ok_or("group id limit reached")?;
                        let weight=self.allocate_group_weight(body,&key);
                        self.groups.push(ReaderGroup{id,tabs:vec![],active:String::new(),weight});self.groups.len()-1
                    } else {(0..self.groups.len()).find(|i|*i!=body).ok_or("split destination missing")?};
                    self.groups[body].tabs.retain(|k|k!=&key);
                    self.groups[target].tabs.push(key.clone());self.groups[target].active=key;
                } else {self.groups[source].active=key;}
                self.groups[body].active="$body".into();self.focus_group=self.groups[body].id;self.maximized=None;
            }
        }
        self.sync_groups()?;
        self.view_epoch=self.view_epoch.saturating_add(1);
        Ok(())
    }

    fn sync_groups(&mut self) -> Result<(), String> {
        self.groups.retain(|g| !g.tabs.is_empty());
        self.ensure_groups();
        if !self.groups.iter().any(|g| g.id == self.focus_group) { self.focus_group = self.groups[0].id; }
        if self.maximized.is_some_and(|id| !self.groups.iter().any(|g| g.id == id)) { self.maximized = None; }
        let group = self.groups.iter().find(|g| g.id == self.focus_group).ok_or("focused group missing")?;
        let active = group.active.clone();
        self.activate_key(&active)?;
        self.active_source = active != "$body";
        self.mode = if self.groups.len() > 1 && self.maximized.is_none() { Mode::Split } else if self.active_source { Mode::Source } else { Mode::Body };
        Ok(())
    }

    fn reader_for(&self, key: &str) -> Option<&Self> {
        if self.has_source() && self.key() == key { Some(self) } else { self.readers.get(key).map(|r| r.as_ref()) }
    }

    fn validate_page(&self, key: &str, page: Option<u64>) -> Result<(), String> {
        if let Some(page) = page {
            let reader = self.reader_for(key).ok_or("body has no source page")?;
            if page == 0 || page > reader.pages.len() as u64 { return Err("source page outside loaded range".into()); }
        }
        Ok(())
    }

    pub fn multi_action(&mut self, r: &Value) -> Result<Value, String> {
        let saved=(self.groups.clone(),self.focus_group,self.maximized,self.next_group,self.closed_tabs.clone(),self.jump_back.clone(),self.mode,self.active_source,self.view_epoch);
        let current=self.has_source().then(||self.key());
        let result=self.apply_multi_action(r);
        if result.is_err() {
            (self.groups,self.focus_group,self.maximized,self.next_group,self.closed_tabs,self.jump_back,self.mode,self.active_source,self.view_epoch)=saved;
            if let Some(key)=current {self.activate_key(&key)?;}
        }
        result
    }

    fn apply_multi_action(&mut self, r: &Value) -> Result<Value, String> {
        self.ensure_groups();
        let kind = r["action"].as_str().ok_or("missing action kind")?;
        let key = r["key"].as_str().unwrap_or("").to_owned();
        match kind {
            "activate" => {
                if !self.known_key(&key) { return Err("unknown reader key".into()); }
                let page = if r.get("page").is_some_and(|v| !v.is_null()) { Some(r["page"].as_u64().ok_or("invalid source page")?) } else { None };
                self.validate_page(&key, page)?;
                if page.is_some() || r["citation"].as_bool() == Some(true) {
                    let g = self.groups.iter().find(|g| g.id == self.focus_group).ok_or("focused group missing")?;
                    let reader = self.reader_for(&g.active);
                    self.jump_back = Some((g.active.clone(), g.id, reader.map_or(0, |s| s.page), reader.and_then(|s| s.positions.get(&s.page).copied()).unwrap_or(Vec2::ZERO)));
                }
                let group = self.groups.iter().position(|g| g.tabs.contains(&key)).unwrap_or_else(|| self.groups.iter().position(|g| g.id == self.focus_group).unwrap_or(0));
                if !self.groups[group].tabs.contains(&key) { self.groups[group].tabs.push(key.clone()); }
                self.closed_tabs.retain(|(closed,_,_)| closed != &key);
                self.groups[group].active = key;
                self.focus_group = self.groups[group].id;
                if self.maximized.is_some() { self.maximized = Some(self.focus_group); }
                self.sync_groups()?;
                if let Some(page) = page { self.set_page(page as usize)?; }
            }
            "move" | "newGroup" => {
                let from = self.groups.iter().position(|g| g.tabs.contains(&key)).ok_or("tab is not open")?;
                let dest = if kind == "newGroup" {
                    if self.groups.len() >= 3 { return Err("at most three reader groups".into()); }
                    if self.next_group == u64::MAX { return Err("group id limit reached".into()); }
                    self.next_group
                } else {
                    let id = r["group"].as_u64().ok_or("missing destination group")?;
                    if !self.groups.iter().any(|g| g.id == id) { return Err("unknown destination group".into()); }
                    id
                };
                let index = match r.get("index") { Some(v) => Some(usize::try_from(v.as_u64().ok_or("invalid tab index")?).map_err(|_|"tab index exceeds platform limit")?), None => None };
                let target_len = self.groups.iter().find(|g| g.id == dest).map_or(0, |g| g.tabs.len() - usize::from(g.tabs.contains(&key)));
                if index.is_some_and(|i| i > target_len) { return Err("tab index out of range".into()); }
                if kind == "newGroup" {
                    let weight=self.allocate_group_weight(from,&key);
                    self.groups.push(ReaderGroup {id:dest,tabs:vec![],active:String::new(),weight});self.next_group += 1;
                }
                let old = &mut self.groups[from];old.tabs.retain(|k| k != &key);
                if old.active == key { old.active = old.tabs.first().cloned().unwrap_or_default(); }
                let target = self.groups.iter_mut().find(|g| g.id == dest).ok_or("destination missing")?;
                target.tabs.insert(index.unwrap_or(target.tabs.len()), key.clone());target.active = key;
                self.focus_group = dest;
                if kind == "newGroup" { self.maximized = None; }
                else if self.maximized.is_some() { self.maximized = Some(dest); }
                self.sync_groups()?;
            }
            "close" => {
                let group = self.groups.iter().position(|g| g.tabs.contains(&key)).ok_or("tab is not open")?;
                let g = &mut self.groups[group];let index = g.tabs.iter().position(|k| k == &key).ok_or("tab missing")?;
                self.closed_tabs.push((key.clone(),g.id,index));if self.closed_tabs.len()>32 {self.closed_tabs.remove(0);}
                g.tabs.remove(index);if g.active == key { g.active = g.tabs.get(index.min(g.tabs.len().saturating_sub(1))).cloned().unwrap_or_default(); }
                self.sync_groups()?;
            }
            "reopen" => {
                let entry = self.closed_tabs.iter().rposition(|(key,_,_)| self.known_key(key) && !self.groups.iter().any(|g| g.tabs.contains(key))).ok_or("no closed tab")?;
                let (key,group,index) = self.closed_tabs.remove(entry);
                let i=self.groups.iter().position(|g| g.id==group).unwrap_or(0);
                let g=&mut self.groups[i];g.tabs.insert(index.min(g.tabs.len()),key.clone());g.active=key;self.focus_group=g.id;
                if self.maximized.is_some() { self.maximized = Some(self.focus_group); }
                self.sync_groups()?;
            }
            "maximize" => {
                let id=match r.get("group"){Some(v)=>v.as_u64().ok_or("invalid group id")?,None=>self.focus_group};
                if !self.groups.iter().any(|g|g.id==id) {return Err("unknown group".into());}
                self.maximized=if self.maximized==Some(id){None}else{Some(id)};self.focus_group=id;self.sync_groups()?;
            }
            "merge" => {
                let id=match r.get("group"){Some(v)=>v.as_u64().ok_or("invalid group id")?,None=>self.focus_group};
                let active=self.groups.iter().find(|g|g.id==id).ok_or("unknown group")?.active.clone();
                let tabs=self.groups.iter().flat_map(|g|g.tabs.iter().cloned()).collect();
                self.groups=vec![ReaderGroup{id,tabs,active,weight:1.0}];self.focus_group=id;self.maximized=None;self.sync_groups()?;
            }
            "returnCitation" => {
                let (key,group,page,offset)=self.jump_back.clone().ok_or("no citation return point")?;
                if !self.known_key(&key) {return Err("return reader no longer loaded".into());}
                let i=self.groups.iter().position(|g|g.tabs.contains(&key)).unwrap_or_else(||self.groups.iter().position(|g|g.id==group).unwrap_or(0));
                if !self.groups[i].tabs.contains(&key){self.groups[i].tabs.push(key.clone());}
                self.groups[i].active=key.clone();self.focus_group=self.groups[i].id;
                if self.maximized.is_some(){self.maximized=Some(self.focus_group);}
                self.sync_groups()?;
                if key!="$body" {self.page=page.min(self.pages.len().saturating_sub(1));self.texture=None;self.positions.insert(self.page,offset);}
                self.jump_back=None;
            }
            _ => return Err("unknown reader action".into()),
        }
        self.view_epoch=self.view_epoch.saturating_add(1);
        Ok(self.layout_snapshot())
    }

    pub fn tabs_status(&self) -> Vec<Value> {
        let mut result=vec![json!({"key":"$body","title":"正文","kind":"body"})];
        for (key,reader) in self.readers.iter().map(|(k,r)|(k.clone(),r.as_ref())).chain(self.has_source().then(||(self.key(),self))) {
            result.push(json!({"key":key,"id":reader.id,"sourceId":reader.id,"version":reader.version,"title":reader.title,"kind":"source","pageCount":reader.pages.len()}));
        }
        result
    }

    pub fn layout_snapshot(&self) -> Value {
        let open:std::collections::BTreeSet<_>=self.groups.iter().flat_map(|g|g.tabs.iter().cloned()).filter(|k|k!="$body").collect();
        let positions:Vec<_>=open.iter().filter_map(|key|self.reader_for(key).map(|r|(key,r))).map(|(key,r)|json!({"key":key,"page":if r.pages.is_empty(){None}else{Some(r.page+1)},"zoom":r.zoom,"markdownRaw":r.markdown_raw,"scroll":r.positions.get(&r.page).map(|p|[p.x,p.y]).unwrap_or([0.0,0.0])})).collect();
        let sources:Vec<_>=open.iter().filter_map(|key|self.reader_for(key).map(|r|json!({"key":key,"id":r.id,"version":r.version}))).collect();
        json!({"groups":self.groups.iter().map(|g|json!({"id":g.id,"tabs":g.tabs,"active":g.active,"weight":g.weight})).collect::<Vec<_>>(),"focusGroup":self.focus_group,"maximized":self.maximized,"positions":positions,"sources":sources})
    }

    pub fn restore_layout(&mut self, r: &Value) -> Result<Value,String> {
        let input=r["groups"].as_array().ok_or("missing groups")?;
        if input.is_empty()||input.len()>3{return Err("layout requires one to three groups".into());}
        let mut groups=Vec::new();let mut ids=std::collections::BTreeSet::new();let mut keys=std::collections::BTreeSet::new();
        for g in input {
            let id=g["id"].as_u64().filter(|id|*id>0&&*id<=9_007_199_254_740_990).ok_or("invalid group id")?;
            if !ids.insert(id){return Err("duplicate group id".into());}
            let tabs=g["tabs"].as_array().ok_or("missing tabs")?;if tabs.is_empty(){return Err("empty group".into());}
            let mut parsed=Vec::new();for tab in tabs{let key=tab.as_str().ok_or("invalid key")?;if !self.known_key(key)||!keys.insert(key.to_owned()){return Err("missing or duplicate reader".into());}parsed.push(key.to_owned());}
            let active=g["active"].as_str().ok_or("missing active tab")?.to_owned();if !parsed.contains(&active){return Err("active tab not in group".into());}
            let weight=g["weight"].as_f64().filter(|v|v.is_finite()&&*v>0.0&&*v<=100.0).ok_or("invalid group weight")? as f32;
            if weight <= 0.0 { return Err("group weight is below supported precision".into()); }
            groups.push(ReaderGroup{id,tabs:parsed,active,weight});
        }
        let focus=r["focusGroup"].as_u64().filter(|id|ids.contains(id)).ok_or("invalid focused group")?;
        let maximized=match r.get("maximized"){Some(Value::Null)|None=>None,Some(v)=>Some(v.as_u64().filter(|id|ids.contains(id)&&*id==focus).ok_or("invalid maximized group")?)};
        let mut positions=Vec::new();let mut seen=std::collections::BTreeSet::new();
        for p in r["positions"].as_array().ok_or("missing positions")? {
            let key=p["key"].as_str().ok_or("missing position key")?;let reader=self.reader_for(key).ok_or("unknown position reader")?;
            if !seen.insert(key.to_owned()){return Err("duplicate position".into());}
            let page_value=p.get("page").ok_or("missing reader page")?;
            let page=if reader.pages.is_empty(){if !page_value.is_null(){return Err("text has no pages".into());}0}else{let n=page_value.as_u64().filter(|n|*n>0&&*n<=reader.pages.len() as u64).ok_or("invalid page")?;(n-1) as usize};
            let zoom=p["zoom"].as_f64().filter(|n|n.is_finite()&&*n>=0.25&&*n<=3.0).ok_or("invalid zoom")? as f32;
            let scroll=p["scroll"].as_array().filter(|a|a.len()==2).ok_or("invalid scroll")?;
            let x=scroll[0].as_f64().filter(|n|n.is_finite()&&*n>=0.0&&*n<=1e8).ok_or("invalid scroll")? as f32;
            let y=scroll[1].as_f64().filter(|n|n.is_finite()&&*n>=0.0&&*n<=1e8).ok_or("invalid scroll")? as f32;
            let markdown_raw=match p.get("markdownRaw"){None=>false,Some(v)=>v.as_bool().ok_or("invalid markdownRaw")?};
            positions.push((key.to_owned(),page,zoom,vec2(x,y),markdown_raw));
        }
        keys.remove("$body");
        if seen!=keys{return Err("positions must match open source tabs".into());}
        let mut source_keys=std::collections::BTreeSet::new();
        for source in r["sources"].as_array().ok_or("missing sources")? {
            let id=source["id"].as_str().ok_or("missing source id")?;
            let version=source["version"].as_str().ok_or("missing source version")?;
            let key=json!([id,version]).to_string();
            if source.get("key").is_some_and(|k|k.as_str()!=Some(key.as_str())){return Err("source key mismatch".into());}
            let reader=self.reader_for(&key).ok_or("source not loaded")?;
            if id!=reader.id||version!=reader.version||!source_keys.insert(key){return Err("source identity mismatch or duplicate".into());}
        }
        if source_keys!=keys{return Err("sources must match open source tabs".into());}
        // Every check precedes mutation; activating validated keys cannot fail.
        for (key,page,zoom,scroll,markdown_raw) in positions {
            let current=self.has_source()&&self.key()==key;
            let reader=if current{&mut *self}else{self.readers.get_mut(&key).ok_or("reader disappeared")?.as_mut()};
            reader.page=page;reader.zoom=zoom;reader.positions.insert(page,scroll);reader.texture=None;reader.markdown_raw=markdown_raw;
        }
        self.groups=groups;self.focus_group=focus;self.maximized=maximized;self.next_group=self.next_group.max(ids.iter().next_back().copied().ok_or("missing group id")?.saturating_add(1));
        self.sync_groups()?;self.view_epoch=self.view_epoch.saturating_add(1);Ok(self.layout_snapshot())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn markdown_raw_snapshot_roundtrip_defaults_and_type_validation() {
        let mut s=loaded();s.markdown_raw=true;
        let snapshot=s.layout_snapshot();
        s.markdown_raw=false;s.restore_layout(&snapshot).unwrap();assert!(s.markdown_raw);
        for invalid in [json!(null),json!("true"),json!(1)] {
            let before=s.layout_snapshot();let mut bad=before.clone();bad["positions"][0]["markdownRaw"]=invalid;
            assert!(s.restore_layout(&bad).is_err());assert_eq!(s.layout_snapshot(),before);
        }
        let mut legacy=snapshot.clone();
        for p in legacy["positions"].as_array_mut().unwrap(){p.as_object_mut().unwrap().remove("markdownRaw");}
        s.restore_layout(&legacy).unwrap();assert!(!s.markdown_raw);
        assert!(s.readers.values().all(|r|!r.markdown_raw));
    }
    #[test]
    fn splitting_maximized_reader_shows_both_groups_and_same_group_move_reorders() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();let b=json!(["b","1"]).to_string();
        s.set_view_mode("source").unwrap();assert!(s.maximized.is_some());
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        assert_eq!(s.groups.len(),2);assert_eq!(s.maximized,None);assert_eq!(s.mode,Mode::Split);
        let body_group=s.groups.iter().find(|g|g.tabs.iter().any(|k|k=="$body")).unwrap().id;
        s.multi_action(&json!({"action":"move","key":b,"group":body_group,"index":0})).unwrap();
        let g=s.groups.iter().find(|g|g.id==body_group).unwrap();assert_eq!(g.tabs,vec![b.clone(),"$body".to_owned()]);
        assert_eq!(g.active,b);assert_eq!(s.groups.iter().flat_map(|g|g.tabs.iter()).filter(|k|k.as_str()=="$body").count(),1);
    }

    #[test]
    fn cached_closed_source_reopens_with_its_exact_version_and_position() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();
        s.multi_action(&json!({"action":"activate","key":a})).unwrap();s.positions.insert(0,vec2(0.0,45.0));
        s.multi_action(&json!({"action":"close","key":a})).unwrap();
        s.multi_action(&json!({"action":"activate","key":a})).unwrap();
        assert_eq!(s.key(),a);assert_eq!(s.positions.get(&0),Some(&vec2(0.0,45.0)));
        assert!(s.closed_tabs.iter().all(|(key,_,_)|key!=&a));
        assert_eq!(s.groups.iter().flat_map(|g|g.tabs.iter()).filter(|key|**key==a).count(),1);
    }

    #[test]
    fn reopened_tab_is_visible_when_another_group_is_maximized() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();let b=json!(["b","1"]).to_string();
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        s.multi_action(&json!({"action":"close","key":b})).unwrap();
        s.multi_action(&json!({"action":"maximize","group":s.focus_group})).unwrap();
        s.multi_action(&json!({"action":"reopen"})).unwrap();
        assert_eq!(s.maximized,Some(s.focus_group));assert_eq!(s.key(),b);
        assert_eq!(s.groups.iter().find(|g|g.id==s.focus_group).unwrap().active,b);
        let before=s.layout_snapshot();let mut invalid=before.clone();invalid["groups"][0]["weight"]=json!(1e-300);
        assert!(s.restore_layout(&invalid).is_err());assert_eq!(s.layout_snapshot(),before);
    }

    #[test]
    fn first_split_prioritizes_body_and_third_group_shares_reading_space() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();
        s.set_view_mode("split").unwrap();
        assert!((s.groups[0].weight-0.62).abs()<0.0001);
        assert!((s.groups[1].weight-0.38).abs()<0.0001);
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        assert_eq!(s.groups.len(),3);
        assert!((s.groups[0].weight-0.62).abs()<0.0001);
        assert!((s.groups[1].weight-0.19).abs()<0.0001);
        assert!((s.groups[2].weight-0.19).abs()<0.0001);
    }

    #[test]
    fn explicit_new_group_and_restored_custom_widths_are_preserved() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();let b=json!(["b","1"]).to_string();
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        assert!((s.groups[0].weight-0.62).abs()<0.0001);
        s.groups[0].weight=0.7;s.groups[1].weight=0.3;
        let snapshot=s.layout_snapshot();s.restore_layout(&snapshot).unwrap();
        s.multi_action(&json!({"action":"activate","key":a})).unwrap();
        s.set_view_mode("split").unwrap();
        assert_eq!(s.groups[0].weight,0.7);assert_eq!(s.groups[1].weight,0.3);
        s.multi_action(&json!({"action":"newGroup","key":b})).unwrap();
        assert_eq!(s.groups[0].weight,0.7);
        assert_eq!(s.groups[1].weight,0.15);assert_eq!(s.groups[2].weight,0.15);
    }

    #[test]
    fn relocating_sole_tab_keeps_weight_and_tiny_weights_stay_positive() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        assert_eq!(s.groups.len(),2);
        assert!((s.groups[1].weight-0.38).abs()<0.0001);
        s.groups[0].weight=f32::from_bits(1);
        let weight=s.allocate_group_weight(0,"$body");
        assert!(weight.is_finite()&&weight>0.0);
        assert!(s.groups[0].weight>0.0);
    }

    fn loaded() -> SourceWorkspace {
        let mut s=SourceWorkspace::default();
        s.load_text("a".into(),"A".into(),"1".into(),"alpha".into()).unwrap();
        s.load_text("b".into(),"B".into(),"1".into(),"beta".into()).unwrap();s
    }
    #[test]
    fn cached_reader_moves_without_recursive_state_or_losing_text() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();
        s.multi_action(&json!({"action":"activate","key":a})).unwrap();assert_eq!(s.text.as_deref(),Some("alpha"));
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();assert_eq!(s.groups.len(),2);
        assert!(s.readers.values().all(|r|r.readers.is_empty()&&r.groups.is_empty()));
        s.multi_action(&json!({"action":"close","key":a})).unwrap();s.multi_action(&json!({"action":"reopen"})).unwrap();assert_eq!(s.text.as_deref(),Some("alpha"));
    }
    #[test]
    fn restore_rejects_duplicate_tabs_and_invalid_positions_atomically() {
        let mut s=loaded();let before=s.layout_snapshot();let mut bad=before.clone();bad["groups"][0]["tabs"].as_array_mut().unwrap().push(json!("$body"));
        assert!(s.restore_layout(&bad).is_err());assert_eq!(s.layout_snapshot(),before);
        let mut bad=before.clone();bad["positions"][0]["zoom"]=json!(-1);assert!(s.restore_layout(&bad).is_err());assert_eq!(s.layout_snapshot(),before);
        s.restore_layout(&before).unwrap();assert_eq!(s.layout_snapshot(),before);
    }

    #[test]
    fn citation_return_restores_prior_reader_scroll_and_body_can_be_hidden() {
        let mut s=loaded();let a=json!(["a","1"]).to_string();let b=json!(["b","1"]).to_string();
        s.multi_action(&json!({"action":"activate","key":a})).unwrap();
        s.positions.insert(0,vec2(0.0,72.0));
        s.multi_action(&json!({"action":"activate","key":b,"citation":true})).unwrap();
        s.multi_action(&json!({"action":"returnCitation"})).unwrap();
        assert_eq!(s.key(),a);assert_eq!(s.positions.get(&0),Some(&vec2(0.0,72.0)));
        s.multi_action(&json!({"action":"close","key":"$body"})).unwrap();
        assert!(!s.groups.iter().any(|g|g.tabs.iter().any(|k|k=="$body")));
        s.set_view_mode("body").unwrap();assert_eq!(s.mode,Mode::Body);assert!(!s.active_source);
    }

    #[test]
    fn versions_have_distinct_keys_and_group_limit_is_atomic() {
        let mut s=loaded();s.load_text("a".into(),"A v2".into(),"2".into(),"new alpha".into()).unwrap();
        let a=json!(["a","1"]).to_string();let newer=json!(["a","2"]).to_string();
        s.multi_action(&json!({"action":"newGroup","key":a})).unwrap();
        s.multi_action(&json!({"action":"newGroup","key":newer})).unwrap();
        let before=s.layout_snapshot();assert!(s.multi_action(&json!({"action":"newGroup","key":"$body"})).is_err());assert_eq!(s.layout_snapshot(),before);
        assert_eq!(s.reader_for(&a).unwrap().text.as_deref(),Some("alpha"));
        assert_eq!(s.reader_for(&newer).unwrap().text.as_deref(),Some("new alpha"));
    }

    #[test]
    fn invalid_actions_and_closed_cache_do_not_poison_persisted_layout() {
        let mut s=loaded();
        for action in [json!({"action":"newGroup","key":"missing"}),json!({"action":"maximize","group":"bad"}),json!({"action":"move","key":"$body","group":1,"index":999})] {
            let before=s.layout_snapshot();let epoch=s.view_epoch;
            assert!(s.multi_action(&action).is_err());assert_eq!(s.layout_snapshot(),before);assert_eq!(s.view_epoch,epoch);
        }
        let a=json!(["a","1"]).to_string();s.multi_action(&json!({"action":"close","key":a})).unwrap();
        let snapshot=s.layout_snapshot();
        assert_eq!(snapshot["positions"].as_array().unwrap().len(),1);
        assert_eq!(snapshot["sources"].as_array().unwrap().len(),1);
        s.restore_layout(&snapshot).unwrap(); // Extra closed cache is not required in persisted positions.
        let mut fresh=SourceWorkspace::default();fresh.load_text("b".into(),"B".into(),"1".into(),"beta".into()).unwrap();
        fresh.restore_layout(&snapshot).unwrap();assert_eq!(fresh.layout_snapshot(),snapshot);
        let mut host_snapshot=snapshot.clone();
        for source in host_snapshot["sources"].as_array_mut().unwrap(){source.as_object_mut().unwrap().remove("key");}
        fresh.restore_layout(&host_snapshot).unwrap();assert_eq!(fresh.layout_snapshot(),snapshot);
    }
}
