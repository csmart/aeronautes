# ADR 0117 — "Possible backstage" collapsed, and a caution wherever a segment store is open

**Status:** Accepted for 0.5.0 (13 November 2026), delivered with this record's series, from finding F45 of the final candidate's test run and the operator's decision of the same day.
**Scope:** the container column's Logical view, the notices above the object listing and the text of the delete confirmations.
No deletion rule changes.

## Context

DESIGN §24 sorts containers by segment evidence.
A container known to hold segments (Confirmed or Declared) is left out of the Logical view and counted in its footer.
A container that is only named like a segment store (`*_segments`, `*-segments`, `segments` or `.segments`) is Suspected: §24.5 keeps it in the list, under a "Possible backstage" heading, because the name is only a guess and the container may be an ordinary one.

In the final candidate's test run the operator found that group too easy to walk into.
Its rows looked like every other row, opening one showed the ordinary listing with the ordinary delete actions, and the listing's one caution ("Backstage: segments of …") appeared only after the inspector's "Open segments backstage" jump.
Someone who deletes objects there can break the large objects whose segments they are.

The application cannot say whether a given object is in use, because no live reverse-reference scan exists (ADR 0044).
So this record says more, earlier, and changes no rule.

## Decision

1. **The "Possible backstage" group is collapsed when the window opens.**
   Its heading and count stay in the list, and the heading is a control that opens and closes the group.
   The rows inside are drawn subdued.
   Opening the group lasts for the window, as the choice of mode does.
   A Suspected container is still never left out of the list the way a known segment store is: collapsing changes how much room the guess takes, not whether the container can be reached (§24.5).
   The All and Backstage modes list every container as before.
2. **A caution stands above the listing whenever the open container is, or may be, a segment store,** however it was reached: a row, a context menu or the inspector's jump.
   For a Suspected container it says the container is named like a segment store; for a Declared or Confirmed one it says the container is one.
   Both say that objects there may be segments of larger objects, and that deleting one can break the object it belongs to: the application does not know whether a given object is a segment, or whether anything still uses it.
   After the inspector's jump the existing notice, which names the manifest, stands in its place.
3. **Every delete confirmation opened in such a container repeats that caution:** the object and marker delete, the manifest delete, the selected-set delete, and the dialog that deletes a prefix, empties a container or deletes it.
4. **The caution is words and nothing else.**
   It rests on the container's classification, which for a Suspected container is its name alone, so it says "may".
   It adds no confirmation, refuses nothing and authorises nothing: ADRs 0052, 0092, 0093, 0094 and 0095 decide what a delete may do, unchanged, and a heuristic still never authorises a destructive segment operation (§24.4).
5. **Not in this record.**
   Leaving Suspected containers out of the list; a check, before a delete, of whether a manifest refers to the object; a setting for the group's default.

## Consequences

- A container that is only named like a segment store takes one more click to reach in the Logical view; the heading says how many there are.
- The inspector's sentence for a Suspected container no longer says "shown, never hidden"; it says where the container is listed.
- An ordinary container that happens to carry such a name shows the caution too.
  That is the cost of a guess from the name, and the wording says "named like".

## Tests

- In the Logical view the group's rows are absent until its heading is pressed, the heading carries the count, and pressing it again closes the group; All and Backstage list the containers as before.
- Opening a Suspected container and a Confirmed one each shows its caution above the listing, and an ordinary container shows none; after the inspector's jump only the jump's notice shows.
- Each of the four delete confirmations shows the caution in such a container and not in an ordinary one.
- A browser journey on every target creates a container named like a segment store, finds its row only after opening the group, and sees the caution above its listing and in its delete dialog.
