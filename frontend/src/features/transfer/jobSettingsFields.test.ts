import { afterEach, expect, it } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import JobSettingsFields from "./JobSettingsFields.svelte";
import { newJobSettings } from "./jobSettings";

let shown: ReturnType<typeof mount> | null = null;
afterEach(async () => {
  if (shown) await unmount(shown);
  shown = null;
  document.body.innerHTML = "";
});

// Each transfer says beside its settings how a confirmed replacement
// decides, in its own terms: the upload's sizes ignored for the whole job
// when a file is segmented, the download's large objects, the copy's listings.
it.each([
  ["upload", "sizes are ignored for the whole upload: a file whose time matches is left even when its size differs"],
  ["download", "its file is replaced whenever the time differs"],
  ["copy", "unless both listings show the same size and ETag"],
] as const)("the %s's settings say how replacing decides", (transfer, rule) => {
  shown = mount(JobSettingsFields, {
    target: document.body,
    props: { settings: newJobSettings("auto"), defaultProfile: undefined, transfer, onchange: () => {} },
  });
  flushSync();
  const text = document.querySelector(".replacing")?.textContent ?? "";
  expect(text).toContain("Replacing");
  expect(text).toContain(rule);
});
