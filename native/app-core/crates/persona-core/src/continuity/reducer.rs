//! One chat's items and how far they are known to be complete (`spec/fixtures/continuity/README.md`).
//!
//! The current iOS `ChatStore` is the behavioral reference. `covered` starts at zero, meaning no baseline. A push
//! that continues from zero sets the baseline. A later push without a baseline waits for the backlog, then asks for
//! a reload once it has arrived. A different history clears the old items and opened-fold evidence. A repeated id
//! takes its newest complete copy, including metadata and receipts; opened folds and supervision promotion survive
//! overlapping pages.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use crate::item::Item;

/// What a batch means for the chat's continuity.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Gap {
    /// Nothing to fetch.
    None,
    /// Lines are missing after `covered`: fetch them immediately.
    Fill,
    /// The chat cannot be stitched together: fetch the latest page instead.
    Reload,
}

/// The fields of one batch of items, as a response or a push carries it.
#[derive(Debug, Default)]
pub struct Batch {
    pub items: Vec<Item>,
    pub history: Option<String>,
    pub from_seq: Option<i64>,
    pub to_seq: Option<i64>,
    pub backlog: bool,
    pub has_more: Option<bool>,
    pub more_after: bool,
}

#[derive(Debug, Default)]
pub struct Reducer {
    /// Ordered by `(seq, block)`, like `ChatStore.ordered`.
    items: Vec<Arc<Item>>,
    ids: HashSet<String>,
    history: Option<String>,
    covered: i64,
    /// `None` until a page gave usable pagination information, and again after a history reset. iOS projects this
    /// onto its Bool as false; the wire retains the missing-information state.
    has_more: Option<bool>,
    loaded_once: bool,
    /// Folds opened here, retained as the page brought them so a repeated page cannot close them again or erase
    /// promotion evidence. A replacement of the transcript forgets them.
    opened: HashMap<String, Arc<Item>>,
}

impl Reducer {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn items(&self) -> &[Arc<Item>] {
        &self.items
    }
    pub fn history(&self) -> Option<&str> {
        self.history.as_deref()
    }
    pub fn covered(&self) -> i64 {
        self.covered
    }
    pub fn has_more(&self) -> Option<bool> {
        self.has_more
    }
    pub fn loaded_once(&self) -> bool {
        self.loaded_once
    }
    pub fn first_seq(&self) -> Option<i64> {
        self.items.first().map(|i| i.seq)
    }
    fn last_seq(&self) -> Option<i64> {
        self.items.last().map(|i| i.seq)
    }

    /// Restores cached state without treating the cache as a received backlog.
    pub fn restore(
        &mut self,
        items: Vec<Item>,
        history: Option<String>,
        covered: i64,
        has_more: Option<bool>,
        loaded_once: bool,
    ) {
        self.clear();
        self.items = items.into_iter().map(Arc::new).collect();
        self.ids = self.items.iter().map(|i| i.id.clone()).collect();
        self.history = history;
        self.covered = covered.max(0);
        self.has_more = has_more;
        self.loaded_once = loaded_once;
    }

    /// Applies a page or a push and says whether lines must be fetched.
    pub fn apply(&mut self, batch: Batch) -> Gap {
        let Batch {
            items,
            history,
            from_seq,
            to_seq,
            backlog,
            has_more,
            more_after,
        } = batch;
        if let (Some(new), Some(old)) = (&history, &self.history)
            && new != old
        {
            self.clear();
            self.history = Some(new.clone());
            self.covered = 0;
            self.has_more = None;
            self.loaded_once = false;
            if from_seq.is_some_and(|f| f > 0) {
                return Gap::Reload;
            }
        }
        if history.is_some() {
            self.history = history.clone();
        }
        let Some(to_seq) = to_seq else {
            self.merge(items, backlog, has_more);
            self.covered = self.covered.max(self.last_seq().unwrap_or(0));
            return if more_after { Gap::Reload } else { Gap::None };
        };
        if let Some(from_seq) = from_seq {
            self.merge(items, backlog, has_more);
            if self.covered == 0 && from_seq > 0 {
                return if self.loaded_once { Gap::Reload } else { Gap::None };
            }
            if from_seq > self.covered {
                return Gap::Fill;
            }
            self.covered = self.covered.max(to_seq);
            return if more_after { Gap::Fill } else { Gap::None };
        }
        if self.covered > 0 && items.first().is_some_and(|first| first.seq > self.covered + 1) {
            self.replace(items, history, Some(to_seq), has_more.unwrap_or(true));
            if backlog {
                self.loaded_once = true;
            }
            return Gap::None;
        }
        self.merge(items, backlog, has_more);
        self.covered = self.covered.max(to_seq);
        Gap::None
    }

    /// A reload's latest page. Pushes of the same history strictly past `to_seq` stay, without claiming coverage
    /// through them. `loaded_once` does not change.
    pub fn replace(&mut self, items: Vec<Item>, history: Option<String>, to_seq: Option<i64>, has_more: bool) {
        let same_history = history.is_none() || history == self.history;
        let keep: Vec<Arc<Item>> = match to_seq {
            Some(to) if same_history => self.items.iter().filter(|i| i.seq > to).cloned().collect(),
            _ => Vec::new(),
        };
        self.clear();
        self.has_more = Some(has_more);
        if history.is_some() {
            self.history = history;
        }
        self.merge(items, false, None);
        for item in keep {
            self.insert_shared(item);
        }
        self.covered = to_seq.unwrap_or_else(|| self.last_seq().unwrap_or(0));
    }

    /// An older page sets pagination directly, without changing history, coverage or `loaded_once`.
    pub fn older(&mut self, items: Vec<Item>, has_more: Option<bool>) {
        if has_more.is_some() {
            self.has_more = has_more;
        }
        self.merge(items, false, None);
    }

    /// Puts an opened fold's work back in its place, all pages at once, leaving out folds in the response. The opened
    /// fold itself is evidence for a later overlapping page, even when the caller held an older, unpromoted copy.
    pub fn unfold(&mut self, fold_id: &str, work: Vec<Item>) {
        let Some(index) = self.items.iter().position(|i| i.id == fold_id) else {
            return;
        };
        let retained = self.items.remove(index);
        self.ids.remove(fold_id);
        self.opened.insert(fold_id.to_owned(), retained);
        self.merge(work.into_iter().filter(|i| i.kind != "fold").collect(), false, None);
    }

    pub(crate) fn find(&self, id: &str) -> Option<&Arc<Item>> {
        self.items.iter().find(|i| i.id == id)
    }

    fn clear(&mut self) {
        self.items.clear();
        self.ids.clear();
        self.opened.clear();
    }

    /// Keeps the last complete copy of each id, preserving the relative order of the surviving items. Promotion is
    /// collected before deduplication, just as `ChatStore.newestCopies` requires for a replayed cache.
    fn newest_shared(items: Vec<Arc<Item>>) -> Vec<Arc<Item>> {
        if items.len() < 2 {
            return items;
        }
        let last: HashSet<usize> = {
            let mut seen = HashSet::new();
            (0..items.len())
                .rev()
                .filter(|index| seen.insert(items[*index].id.clone()))
                .collect()
        };
        items
            .into_iter()
            .enumerate()
            .filter(|(index, _)| last.contains(index))
            .map(|(_, item)| item)
            .collect()
    }

    fn newest(items: Vec<Item>) -> Vec<Item> {
        if items.len() < 2 {
            return items;
        }
        let last: HashSet<usize> = {
            let mut seen = HashSet::new();
            (0..items.len())
                .rev()
                .filter(|index| seen.insert(items[*index].id.clone()))
                .collect()
        };
        items
            .into_iter()
            .enumerate()
            .filter(|(index, _)| last.contains(index))
            .map(|(_, item)| item)
            .collect()
    }

    fn still_open(&self, item: &Item) -> bool {
        let (Some(range), Some(was)) = (&item.fold, self.opened.get(&item.id).and_then(|i| i.fold.as_ref())) else {
            return false;
        };
        item.kind == "fold"
            && (range.through_seq, crate::item::part(&range.through_id))
                <= (was.through_seq, crate::item::part(&was.through_id))
    }

    fn merge(&mut self, new: Vec<Item>, backlog: bool, has_more: Option<bool>) {
        let new: Vec<Item> = new
            .into_iter()
            .map(|mut item| {
                item.public_refusal();
                item
            })
            .collect();
        let original_len = new.len();
        let new_min = new.iter().map(|i| i.seq).min();
        let mut evidence: Vec<Arc<Item>> = self
            .items
            .iter()
            .chain(self.opened.values())
            .filter(|item| item.has_promotion())
            .cloned()
            .collect();
        evidence.extend(new.iter().filter(|item| item.has_promotion()).cloned().map(Arc::new));
        let deduplicated = self.items.len() != self.ids.len();
        if deduplicated {
            self.items = Self::newest_shared(std::mem::take(&mut self.items));
            self.ids = self.items.iter().map(|i| i.id.clone()).collect();
        }
        let before_retention = self.items.clone();
        if !evidence.is_empty() {
            self.items =
                Self::retain_promotion(self.items.iter().map(|item| item.as_ref().clone()).collect(), &evidence);
        }
        let changed = deduplicated || self.items != before_retention;
        let promoted_new: Vec<Item> = Self::newest(new);
        let fresh: Vec<Item> = Self::retain_promotion(promoted_new, &evidence)
            .into_iter()
            .map(|item| Arc::into_inner(item).expect("a newly retained item has one owner"))
            .filter(|item| !self.still_open(item))
            .collect();
        let batch_work: Vec<Item> = fresh
            .iter()
            .filter(|item| item.is_work() && item.kind != "fold")
            .cloned()
            .collect();
        let fresh: Vec<Item> = fresh
            .into_iter()
            .filter(|item| !(item.kind == "fold" && batch_work.iter().any(|work| item.folds(work))))
            .collect();

        // A changed fold replaces work it covers; real overlapping work replaces an old fold even when its page is
        // only partial. Neither operation may discard the evidence retained above.
        let folds: Vec<&Item> = fresh
            .iter()
            .filter(|item| item.kind == "fold" && !self.find(&item.id).is_some_and(|old| old.same_fold(item)))
            .collect();
        let work: Vec<&Item> = fresh
            .iter()
            .filter(|item| item.is_work() && item.kind != "fold")
            .collect();
        let before = self.items.len();
        self.items.retain(|item| {
            !(item.is_work() && folds.iter().any(|fold| fold.folds(item)))
                && !(item.kind == "fold" && work.iter().any(|work| item.folds(work)))
        });
        self.ids = self.items.iter().map(|i| i.id.clone()).collect();
        let mut changed = changed || self.items.len() != before;
        let indices: HashMap<String, usize> = self
            .items
            .iter()
            .enumerate()
            .map(|(index, item)| (item.id.clone(), index))
            .collect();
        let mut in_order = !deduplicated;
        for item in fresh {
            if let Some(&index) = indices.get(item.id.as_str()) {
                if self.items[index].as_ref() != &item {
                    if self.items[index].key() != item.key() {
                        in_order = false;
                    }
                    self.items[index] = Arc::new(item);
                    changed = true;
                }
                continue;
            }
            if self.items.last().is_some_and(|last| item.key() < last.key()) {
                in_order = false;
            }
            self.ids.insert(item.id.clone());
            self.items.push(Arc::new(item));
            changed = true;
        }
        if changed && !in_order {
            self.items.sort_by_key(|item| item.key());
        }
        // The comparison is against the list after fold removal, not a saved first item from before it. When the
        // list is empty, a non-empty page reaches its start (iOS's `.max` fallback).
        let reaches_start = new_min.is_some_and(|min| min <= self.first_seq().unwrap_or(i64::MAX));
        if let Some(more) = has_more
            && backlog
            && (!self.loaded_once || self.items.len() <= original_len)
            && (more || reaches_start)
        {
            self.has_more = Some(more);
        }
        if backlog {
            self.loaded_once = true;
        }
    }

    fn retain_promotion(items: Vec<Item>, evidence: &[Arc<Item>]) -> Vec<Arc<Item>> {
        if evidence.is_empty() {
            return items.into_iter().map(Arc::new).collect();
        }
        let promoted_ids: HashSet<&str> = evidence
            .iter()
            .filter(|item| item.supervision_user)
            .map(|item| item.id.as_str())
            .collect();
        let folds: Vec<&Arc<Item>> = evidence.iter().filter(|item| item.fold.is_some()).collect();
        let work: Vec<&Arc<Item>> = evidence.iter().filter(|item| item.is_work()).collect();
        items
            .into_iter()
            .map(|mut item| {
                if promoted_ids.contains(item.id.as_str())
                    || (item.is_work() && folds.iter().any(|fold| fold.folds(&item)))
                    || (item.fold.is_some() && work.iter().any(|work| item.folds(work)))
                {
                    item.set_supervision();
                }
                Arc::new(item)
            })
            .collect()
    }

    fn insert_shared(&mut self, item: Arc<Item>) {
        if !self.ids.insert(item.id.clone()) {
            return;
        }
        let key = item.key();
        let index = if self.items.last().is_none_or(|last| last.key() <= key) {
            self.items.len()
        } else {
            self.items.partition_point(|i| i.key() <= key)
        };
        self.items.insert(index, item);
    }
}
