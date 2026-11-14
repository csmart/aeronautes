import { test, type Page } from "@playwright/test";
import { basename } from "node:path";
import { control } from "../control.ts";
import {
  armDelete, connect, expect, finish, inspector, localFolder, objectRow, openContainer, openFolder, openUpload, ownContainer, typedDelete,
  writeFiles,
} from "../helpers.ts";

// ADR 0120: a large object's segments go with it unless the operator says
// otherwise, and stay wherever versions are kept. No journey here touches
// the option: each reads how it stands. What a deletion left is read by the
// run's own session, which lists the containers the run made, and a listing
// can lag the deletes on a real store, so each is asked again within a bound.
const lag = { timeout: 3 * 60 * 1000 };
// Three chunks at the upload's chunk size, so the file uploads in three
// segments.
const large = 3 << 20;
const segmentsOfLarge = 3;
const manifestKind = { static: "SLO", dynamic: "DLO" };

test.describe.serial("segments without versioning", () => {
  let container = "";

  test.beforeAll(async ({}, info) => {
    container = await ownContainer(info.project.name, "ui-segments", true);
  });

  test("delete a static and a dynamic large object, each with its segments", async ({ page }, info) => {
    const target = info.project.name;
    const sidecar = `${container}_segments`;
    await openContainer(page, target, container);
    // One file by each choice. A store that does not return static
    // manifests publishes both as dynamic (ADR 0111 decision 10), so the
    // kind deleted below is the kind each job recorded.
    const uploads = [];
    for (const choice of ["slo", "dlo"] as const) {
      const before = await control.names(target, sidecar);
      const upload = await uploadFolder(page, [large], choice);
      uploads.push({ ...upload, segments: await segmentsAdded(target, sidecar, before, segmentsOfLarge) });
    }

    // Each goes from its own dialog, where the option starts ticked and the
    // object's name is typed. The other's segments must still be there.
    let remaining = uploads.flatMap((upload) => upload.segments);
    for (const upload of uploads) {
      await openContainer(page, target, container);
      await openFolder(page, upload.folder);
      const dialog = await openManifestDelete(page, "file00.bin", upload.kind);
      await expect(dialog.getByRole("checkbox")).toBeChecked();
      const confirm = dialog.getByLabel("Type the object name to arm it:");
      await confirm.fill((await confirm.getAttribute("placeholder"))!);
      await dialog.getByRole("button", { name: "Delete manifest + segments" }).click();
      await expect(dialog).toHaveCount(0);

      remaining = remaining.filter((name) => !upload.segments.includes(name));
      await expect.poll(async () => (await control.names(target, sidecar)).sort(), lag).toEqual([...remaining].sort());
      await expect.poll(() => control.names(target, container), lag).not.toContain(`${basename(upload.folder)}/file00.bin`);
    }
  });

  test("delete the container with the segments of its large object", async ({ page }, info) => {
    const target = info.project.name;
    const sidecar = `${container}_segments`;
    await openContainer(page, target, container);
    await uploadFolder(page, [large]);
    await segmentsAdded(target, sidecar, [], segmentsOfLarge);

    await openContainer(page, target, container);
    await page.getByRole("button", { name: "Empty or delete container…" }).click();
    const dialog = page.getByRole("dialog", { name: "Delete" });
    await dialog.getByRole("radio", { name: /^Delete container/ }).check();
    await armDelete(page, "Delete container", "ticked");
    await expect(dialog.getByRole("heading", { name: "Deletion complete" })).toBeVisible({ timeout: 8 * 60 * 1000 });
    await expect.poll(() => control.names(target, sidecar), lag).toEqual([]);
    await expectNotListed(page, target, container);
  });
});

test.describe.serial("segments where versions are kept", () => {
  let live = "";
  let archive = "";
  // What the store files in the archive for each name deleted from the live
  // container: the object and a deletion marker.
  const versions = async (target: string, name: string) => {
    // A version is filed under three hex digits of its name's length, the
    // name, a slash and a timestamp.
    const filedUnder = `${name.length.toString(16).padStart(3, "0")}${name}/`;
    return (await control.names(target, archive)).filter((entry) => entry.startsWith(filedUnder)).length;
  };

  test.beforeEach(({}, info) => {
    test.skip(
      info.project.name !== "swift",
      "only the Swift cloud keeps versions: the mock shows a mode and archives nothing, and the Ceph cloud offers none",
    );
  });

  test.beforeAll(async ({}, info) => {
    if (info.project.name !== "swift") return;
    live = await ownContainer(info.project.name, "ui-history", true);
    archive = await ownContainer(info.project.name, "ui-archive");
  });

  test("a history location keeps what is deleted, and every segment", async ({ page }, info) => {
    const target = info.project.name;
    const sidecar = `${live}_segments`;
    await openContainer(page, target, live);
    await page.getByRole("button", { name: "Configure container" }).click();
    await page.getByRole("combobox", { name: "Versioning" }).selectOption("history_location");
    await page.getByLabel("Archive container").fill(archive);
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Configure container" })).toBeVisible();

    // A small file and a large one by the static choice, and a large one by
    // the dynamic choice, which the container deletion below still finds.
    const first = await uploadFolder(page, [1024, large], "slo");
    const second = await uploadFolder(page, [large], "dlo");
    const segments = (await segmentsAdded(target, sidecar, [], 2 * segmentsOfLarge)).sort();
    const folder = `${basename(first.folder)}/`;

    await openContainer(page, target, live);
    await openFolder(page, first.folder);
    await objectRow(page, "file00.bin").click();
    await inspector(page).getByRole("button", { name: "Delete object…" }).click();
    const objectDialog = page.getByRole("alertdialog", { name: "Delete object" });
    await expect(objectDialog.getByText(`This container keeps history in ${archive}`)).toBeVisible();
    await objectDialog.getByRole("button", { name: "Delete object" }).click();
    await expect(objectRow(page, "file00.bin")).toHaveCount(0);
    await expect.poll(() => versions(target, `${folder}file00.bin`), lag).toBe(2);

    // The large object's dialog does not offer its segments here, and says
    // why: the version the store keeps still uses them.
    const dialog = await openManifestDelete(page, "file01.bin", first.kind);
    await expect(dialog.getByText("Its segments cannot be deleted with it here: this container keeps versions")).toBeVisible();
    await expect(dialog.getByRole("checkbox")).toHaveCount(0);
    await dialog.getByRole("button", { name: "Delete manifest only" }).click();
    await expect(dialog).toHaveCount(0);
    await expect(objectRow(page, "file01.bin")).toHaveCount(0);
    await expect.poll(() => versions(target, `${folder}file01.bin`), lag).toBe(2);
    expect((await control.names(target, sidecar)).sort()).toEqual(segments);

    // Deleting the container is not offered the option either. What it held
    // goes to the archive, the container goes, and every segment stays.
    await openContainer(page, target, live);
    await page.getByRole("button", { name: "Empty or delete container…" }).click();
    const bulk = page.getByRole("dialog", { name: "Delete" });
    await bulk.getByRole("radio", { name: /^Delete container/ }).check();
    await bulk.getByRole("button", { name: "Preview" }).click();
    await expect(bulk.getByText("Their segments cannot be deleted with them here: this container keeps versions")).toBeVisible();
    await armDelete(page, "Delete container");
    await expect(bulk.getByRole("heading", { name: "Deletion complete" })).toBeVisible({ timeout: 8 * 60 * 1000 });
    await expect.poll(() => versions(target, `${basename(second.folder)}/file00.bin`), lag).toBe(2);
    expect((await control.names(target, sidecar)).sort()).toEqual(segments);
    await expectNotListed(page, target, live);
  });

  test("deleting the archive takes the versions and their segments", async ({ page }, info) => {
    const target = info.project.name;
    // No container keeps its versions here any more, so the option starts
    // ticked, and each archived large object goes with its segments.
    // The dialog closes itself once its container is gone, since no segments
    // container is named after the archive, so the job is followed in the
    // drawer.
    await openContainer(page, target, archive);
    await page.getByRole("button", { name: "Empty or delete container…" }).click();
    await page.getByRole("dialog", { name: "Delete" }).getByRole("radio", { name: /^Delete container/ }).check();
    await typedDelete(page, "Delete container", "ticked");
    await expect.poll(() => control.names(target, `${live}_segments`), lag).toEqual([]);
    await expectNotListed(page, target, archive);
  });
});

// uploadFolder uploads a new local folder of files of the given sizes into
// the open container and returns the folder and the kind of large object the
// job recorded it published.
async function uploadFolder(page: Page, sizes: number[], largeObjects?: "slo" | "dlo") {
  const folder = localFolder();
  writeFiles(folder, sizes, 1);
  const dialog = await openUpload(page, largeObjects);
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  const kind = /largeObjects\s*(static|dynamic)/.exec(await finish(page, dialog))?.[1] as "static" | "dynamic";
  expect(kind, "the job's recorded large-object kind").toMatch(/^(static|dynamic)$/);
  await page.getByRole("button", { name: "Refresh listing" }).click();
  return { folder, kind };
}

// segmentsAdded waits until the run's own listing of a segments container
// shows the segments an upload wrote, and returns the names it did not hold
// before.
async function segmentsAdded(target: string, sidecar: string, before: string[], count: number): Promise<string[]> {
  let added: string[] = [];
  await expect.poll(async () => {
    added = (await control.names(target, sidecar)).filter((name) => !before.includes(name));
    return added.length;
  }, lag).toBe(count);
  return added;
}

// openManifestDelete opens the delete dialog of a large object in the open
// folder, which names the kind of manifest it is about to delete.
async function openManifestDelete(page: Page, name: string, kind: "static" | "dynamic") {
  await objectRow(page, name).click();
  await inspector(page).getByRole("button", { name: "Delete manifest…" }).click();
  const dialog = page.getByRole("alertdialog", { name: "Delete manifest" });
  await expect(dialog.getByRole("heading", { name: `Delete ${manifestKind[kind]} manifest` })).toBeVisible();
  return dialog;
}

async function expectNotListed(page: Page, target: string, container: string) {
  await connect(page, target);
  await page.getByRole("tab", { name: "All" }).click();
  await expect(page.getByRole("button", { name: new RegExp(`^${container} `) })).toHaveCount(0);
}
