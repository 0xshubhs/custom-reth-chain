//! Datadir housekeeping that has to happen before reth opens the database.
//!
//! `--archive` must mean "keep everything". reth takes its prune settings from
//! the command line when any prune flag is given and otherwise falls back to
//! `<datadir>/reth.toml` (`launch/common.rs`, `prune_config`). An archive run
//! passes no prune flags, so a datadir that was ever started without
//! `--archive` (where reth wrote full-node prune segments into reth.toml) keeps
//! pruning: receipts and account/storage history older than 10,064 blocks are
//! deleted, silently, once the chain is that long. There is no command-line
//! flag that overrides the file, so the node clears the file's segments itself.

use std::{fs, io, path::Path};

/// Removes every prune segment from a reth.toml and keeps the `[prune.segments]`
/// table itself empty, which is exactly what reth writes for an archive node.
/// Everything outside the prune segments is left byte for byte as it was.
///
/// Returns the new text and the names of the removed segments, or `None` when
/// there was nothing to remove.
pub fn strip_prune_segments(toml: &str) -> Option<(String, Vec<String>)> {
    let mut out = String::with_capacity(toml.len());
    let mut removed = Vec::new();
    // Which table the current line belongs to.
    let mut in_segments_table = false; // [prune.segments]
    let mut in_segment_subtable = false; // [prune.segments.<name>]

    for line in toml.split_inclusive('\n') {
        let trimmed = line.trim();
        if trimmed.starts_with('[') {
            let name = trimmed.trim_start_matches('[').trim_end_matches(']').trim();
            in_segments_table = name == "prune.segments";
            in_segment_subtable = name.starts_with("prune.segments.");
            if in_segment_subtable {
                removed.push(name.trim_start_matches("prune.segments.").to_string());
                continue;
            }
            out.push_str(line);
            continue;
        }
        if in_segment_subtable {
            continue;
        }
        if in_segments_table && !trimmed.is_empty() && !trimmed.starts_with('#') {
            if let Some((key, _)) = trimmed.split_once('=') {
                removed.push(key.trim().to_string());
            }
            continue;
        }
        out.push_str(line);
    }

    if removed.is_empty() {
        return None;
    }
    // Keep one blank line between the emptied table and whatever follows, as
    // reth's own archive file has.
    let tidy = out.replace("[prune.segments]\n[", "[prune.segments]\n\n[");
    Some((tidy, removed))
}

/// Applies [`strip_prune_segments`] to `<datadir>/reth.toml` in place, keeping
/// the previous file as `reth.toml.pruned.bak`. A missing file (a new datadir)
/// is fine: reth writes an archive file itself on first start.
///
/// Returns the removed segment names, or `None` when nothing was changed.
pub fn force_archive_prune_config(datadir: &Path) -> io::Result<Option<Vec<String>>> {
    let path = datadir.join("reth.toml");
    let text = match fs::read_to_string(&path) {
        Ok(t) => t,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    let Some((new_text, removed)) = strip_prune_segments(&text) else {
        return Ok(None);
    };
    fs::write(datadir.join("reth.toml.pruned.bak"), &text)?;
    // Write to a temporary file first so a crash never leaves half a config.
    let tmp = datadir.join("reth.toml.tmp");
    fs::write(&tmp, &new_text)?;
    fs::rename(&tmp, &path)?;
    Ok(Some(removed))
}

#[cfg(test)]
mod tests {
    use super::*;

    const FULL_NODE: &str = "[stages.era]\n\n[prune]\nblock_interval = 5\nminimum_pruning_distance = 10064\n\n[prune.segments]\nsender_recovery = \"full\"\n\n[prune.segments.receipts]\ndistance = 10064\n\n[prune.segments.account_history]\ndistance = 10064\n\n[prune.segments.storage_history]\ndistance = 10064\n\n[prune.segments.bodies_history]\nbefore = 0\n\n[peers]\nrefill_slots_interval = \"5s\"\ntrusted_nodes = []\n";

    const ARCHIVE: &str = "[stages.era]\n\n[prune]\nblock_interval = 5\nminimum_pruning_distance = 10064\n\n[prune.segments]\n\n[peers]\nrefill_slots_interval = \"5s\"\ntrusted_nodes = []\n";

    #[test]
    fn full_node_file_becomes_the_archive_file() {
        let (out, removed) = strip_prune_segments(FULL_NODE).expect("segments to remove");
        assert_eq!(out, ARCHIVE);
        assert_eq!(
            removed,
            vec![
                "sender_recovery",
                "receipts",
                "account_history",
                "storage_history",
                "bodies_history"
            ]
        );
    }

    #[test]
    fn archive_file_is_left_alone() {
        assert!(strip_prune_segments(ARCHIVE).is_none());
    }

    #[test]
    fn rewrites_in_place_and_keeps_a_backup() {
        let dir = std::env::temp_dir().join(format!("meow-prune-{}", std::process::id()));
        fs::create_dir_all(&dir).unwrap();
        fs::write(dir.join("reth.toml"), FULL_NODE).unwrap();
        let removed = force_archive_prune_config(&dir).unwrap().expect("changed");
        assert_eq!(removed.len(), 5);
        assert_eq!(fs::read_to_string(dir.join("reth.toml")).unwrap(), ARCHIVE);
        assert_eq!(
            fs::read_to_string(dir.join("reth.toml.pruned.bak")).unwrap(),
            FULL_NODE
        );
        // A second start changes nothing.
        assert!(force_archive_prune_config(&dir).unwrap().is_none());
        fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn missing_file_is_fine() {
        let dir = std::env::temp_dir().join(format!("meow-prune-none-{}", std::process::id()));
        fs::create_dir_all(&dir).unwrap();
        assert!(force_archive_prune_config(&dir).unwrap().is_none());
        fs::remove_dir_all(&dir).unwrap();
    }
}
