//! Width allocation for the single-row surface, using the ribbon's three existing levels.

#[derive(Clone, Debug, PartialEq)]
pub struct CompactGroup {
    pub id: &'static str,
    pub widths: [f32; 3],
    pub required: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct CompactPlan {
    pub levels: Vec<u8>,
    pub context_menu: bool,
    pub essential_menu: bool,
    pub used: f32,
}

pub fn plan(groups: &[CompactGroup], avail: f32, has_context: bool) -> CompactPlan {
    use crate::ribbon_fit::{FULL, COMPACT, COLLAPSED};
    let safe = |w: f32| if w.is_finite() { w.clamp(0.0, 1_000_000.0) } else { 0.0 };
    let avail = safe(avail);
    let width = |g: &CompactGroup, level: u8| {
        // A hostile measurement cannot make a smaller level wider or non-finite.
        g.widths.iter().take(usize::from(level) + 1).copied().map(safe).fold(f32::MAX, f32::min)
    };
    let total = |levels: &[u8]| {
        groups.iter().zip(levels).map(|(g, &l)| width(g, l)).sum::<f32>() + groups.len().saturating_sub(1) as f32 * 8.0
    };
    let mut levels = vec![FULL; groups.len()];
    // Optional insertion first, then secondary font/paragraph controls, then font names and
    // styles. The active object's small menu precedes loss of paste, size and bold.
    for (id, level) in [
        ("insert", COMPACT), ("insert", COLLAPSED), ("font", COMPACT), ("paragraph", COMPACT),
        ("styles", COMPACT), ("context", COLLAPSED), ("font", COLLAPSED),
        ("paragraph", COLLAPSED), ("styles", COLLAPSED), ("clipboard", COMPACT), ("clipboard", COLLAPSED),
    ] {
        if total(&levels) <= avail { break; }
        if let Some(i) = groups.iter().position(|g| g.id == id) && let Some(l) = levels.get_mut(i) { *l = level; }
    }
    if total(&levels) > avail {
        for (group, level) in groups.iter().zip(&mut levels) {
            if !group.required { *level = COLLAPSED; }
        }
    }
    let essential_menu = total(&levels) > avail;
    let context_menu = has_context && (essential_menu || groups.iter().zip(&levels).any(|(g, &l)| g.id == "context" && l == COLLAPSED));
    let used = if essential_menu {
        // Physically smaller than these two buttons is below the usable viewport minimum.
        // Report their real extent (do not invent a negative/zero-width All Tools button).
        let fixed: Vec<_> = groups.iter().filter(|g| matches!(g.id, "all-tools" | "overflow")).collect();
        fixed.iter().map(|g| width(g, FULL)).sum::<f32>() + fixed.len().saturating_sub(1) as f32 * 8.0
    } else { total(&levels) };
    CompactPlan { levels, context_menu, essential_menu, used }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ribbon_fit::{FULL, COMPACT, COLLAPSED};

    fn groups() -> Vec<CompactGroup> {
        vec![
            CompactGroup { id: "clipboard", widths: [150.0, 90.0, 48.0], required: true },
            CompactGroup { id: "styles", widths: [260.0, 120.0, 48.0], required: false },
            CompactGroup { id: "font", widths: [360.0, 180.0, 48.0], required: false },
            CompactGroup { id: "paragraph", widths: [280.0, 120.0, 48.0], required: false },
            CompactGroup { id: "insert", widths: [96.0, 48.0, 48.0], required: false },
            CompactGroup { id: "context", widths: [320.0, 192.0, 96.0], required: false },
            CompactGroup { id: "overflow", widths: [28.0, 28.0, 28.0], required: true },
            CompactGroup { id: "all-tools", widths: [96.0, 96.0, 96.0], required: true },
        ]
    }

    #[test]
    fn compact_plan_keeps_all_tools_and_never_produces_hostile_geometry() {
        let groups = groups();
        assert!(plan(&groups, 1920.0, true).levels.iter().all(|&l| l == FULL));
        let narrow = plan(&groups, 800.0, true);
        assert!(narrow.used.is_finite() && narrow.used >= 0.0 && narrow.used <= 800.0);
        assert!(narrow.context_menu);
        for avail in [1920.0, 1024.0, 800.0, 180.0, 0.0, -1.0, f32::NAN, f32::INFINITY] {
            let p = plan(&groups, avail, true);
            assert_eq!(p.levels.len(), groups.len());
            assert_eq!(p.levels.last(), Some(&FULL));
            assert!(p.used.is_finite() && p.used >= 0.0);
        }
        assert!(plan(&groups, 180.0, true).essential_menu);
        let mut hostile = groups;
        hostile[1].widths = [f32::NAN, -1.0, f32::INFINITY];
        assert!(plan(&hostile, 800.0, true).used.is_finite());
    }

    #[test]
    fn compact_plan_shrinks_insert_before_secondary_font_and_keeps_essential_controls() {
        let groups = groups();
        let full = groups.iter().map(|g| g.widths[0]).sum::<f32>() + 8.0 * (groups.len() - 1) as f32;
        let p = plan(&groups, full - 20.0, false);
        assert!(p.levels[4] >= COMPACT);
        assert_eq!(p.levels[2], FULL);
        assert_eq!(p.levels[0], FULL);
        let p = plan(&groups, 800.0, true);
        assert_eq!(p.levels[0], FULL);
        assert_eq!(p.levels[5], COLLAPSED);
        // Measured CJK labels can be much wider; they still enter the same plan.
        let mut cjk = groups;
        cjk[1].widths[0] = 540.0;
        assert!(plan(&cjk, 800.0, true).used <= 800.0);
    }
}
