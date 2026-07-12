import { expect, test, type Browser, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

const bootstrapEmail = process.env.E2E_ADMIN_EMAIL ?? 'admin@example.com';
const bootstrapPassword = process.env.E2E_ADMIN_PASSWORD ?? 'replace-with-a-unique-12+-character-password';

test('admin setup, dual-session collaboration, reconnect, viewer permissions, and image persistence', async ({ browser }) => {
  test.setTimeout(150_000);
  const suffix = Date.now().toString(36);
  const editorEmail = `editor-${suffix}@example.com`;
  const viewerEmail = `viewer-${suffix}@example.com`;
  const editorName = 'E2E Editor With An Intentionally Very Long Display Name For Layout Validation';
  const editorInitial = 'Editor-one-time-2026!';
  const viewerInitial = 'Viewer-one-time-2026!';
  const projectName = `E2E Project ${suffix}`;
  const boardName = `Collaboration ${suffix}`;
  const pageErrors: Error[] = [];

  const adminContext = await contextWithoutRandomUUID(browser);
  const admin = await adminContext.newPage();
  admin.on('pageerror', (error) => pageErrors.push(error));
  await login(admin, bootstrapEmail, bootstrapPassword, 'Admin-private-password-2026!');
  expect(await admin.evaluate(() => typeof globalThis.crypto.randomUUID)).toBe('undefined');

  await admin.goto('/admin');
  await createUser(admin, editorEmail, editorName, editorInitial);
  await createUser(admin, viewerEmail, 'E2E Viewer', viewerInitial);

  await admin.setViewportSize({ width: 320, height: 800 });
  const viewerAdminRow = admin.locator('.admin-user-row', { hasText: viewerEmail });
  await viewerAdminRow.getByRole('button', { name: 'Reset password' }).click();
  await expect(viewerAdminRow.locator('.admin-password-reset')).toBeVisible();
  await expectNoPageOverflow(admin);
  await viewerAdminRow.getByRole('button', { name: 'Cancel' }).click();
  await viewerAdminRow.getByRole('button', { name: `Edit display name for ${viewerEmail}` }).click();
  await expect(viewerAdminRow.locator('.admin-name-form')).toBeVisible();
  await expectNoPageOverflow(admin);
  await viewerAdminRow.getByRole('button', { name: 'Cancel' }).click();
  await admin.setViewportSize({ width: 1280, height: 720 });

  await admin.goto('/projects');
  await admin.getByPlaceholder('Project name').fill(projectName);
  await admin.getByPlaceholder('Description (optional)').fill('Playwright collaboration project');
  await admin.getByRole('button', { name: 'Create' }).click();
  await expect(admin.getByRole('heading', { name: projectName })).toBeVisible();
  const projectURL = admin.url();

  await admin.getByPlaceholder('Board name').fill(boardName);
  await admin.getByRole('button', { name: 'Board' }).click();
  await expect(admin.locator('.board-open', { hasText: boardName })).toBeVisible();

  await addMember(admin, editorEmail, 'editor', 'Intentionally Very Long', true);
  await addMember(admin, viewerEmail, 'viewer');

  const editorMemberRow = admin.locator('.member-row', { hasText: editorEmail });
  const desktopMemberLayout = await editorMemberRow.evaluate((row) => {
    const identity = row.querySelector('.member-identity')?.getBoundingClientRect();
    const role = row.querySelector('.member-role')?.getBoundingClientRect();
    return identity && role ? { identityRight: identity.right, roleLeft: role.left } : null;
  });
  expect(desktopMemberLayout).not.toBeNull();
  expect(desktopMemberLayout!.identityRight).toBeLessThanOrEqual(desktopMemberLayout!.roleLeft + 1);

  await admin.setViewportSize({ width: 375, height: 800 });
  await expectNoPageOverflow(admin);
  const memberLayout = await editorMemberRow.evaluate((row) => {
    const identity = row.querySelector('.member-identity')?.getBoundingClientRect();
    const role = row.querySelector('.member-role')?.getBoundingClientRect();
    return identity && role ? { identityBottom: identity.bottom, roleTop: role.top } : null;
  });
  expect(memberLayout).not.toBeNull();
  expect(memberLayout!.identityBottom).toBeLessThanOrEqual(memberLayout!.roleTop + 1);
  await admin.setViewportSize({ width: 1280, height: 720 });

  await admin.locator('.board-open', { hasText: boardName }).click();
  await expect(admin).toHaveURL(/\/boards\//);
  const boardURL = admin.url();
  await expect(admin.locator('.editor-shell')).toBeVisible();
  await expect(admin.locator('.title-block')).toContainText('synced');

  const editorContext = await contextWithoutRandomUUID(browser);
  const editor = await editorContext.newPage();
  editor.on('pageerror', (error) => pageErrors.push(error));
  await login(editor, editorEmail, editorInitial, 'Editor-private-password-2026!');
  await editor.goto(projectURL);
  await editor.locator('.board-open', { hasText: boardName }).click();
  await expect(editor.locator('.title-block')).toContainText('synced');
  await editor.getByTitle('Text').click();
  await expect(editor.getByTitle('Text')).toHaveClass(/selected/);
  await editor.locator('.canvas').click({ position: { x: 420, y: 260 } });
  const editorText = editor.locator('.block-text textarea').first();
  await expect(editorText).toBeVisible({ timeout: 10_000 });
  await editorText.fill('shared seed');
  await editor.keyboard.press('Escape');
  await expect(editor.locator('.title-block')).toContainText('synced');
  await expect(admin.locator('.block-text textarea').first()).toHaveValue('shared seed');

  const adminText = admin.locator('.block-text textarea').first();
  await Promise.all([
    editorText.fill('alpha from editor'),
    adminText.fill('beta from admin')
  ]);
  await expect.poll(async () => {
    const left = await editorText.inputValue();
    const right = await adminText.inputValue();
    return left && left === right ? left : '';
  }, { timeout: 15_000 }).not.toBe('');

  const converged = await editorText.inputValue();
  try {
    await compose('stop', 'api');
    await expect(editor.locator('.title-block')).toContainText('offline', { timeout: 20_000 });
    await editorText.fill(`${converged} offline replay`);
  } finally {
    await compose('up', '-d', 'api');
  }
  await expect.poll(async () => fetch(`${process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080'}/readyz`).then((response) => response.status).catch(() => 0), { timeout: 30_000 }).toBe(200);
  await expect(editor.locator('.title-block')).toContainText('synced', { timeout: 20_000 });
  await expect(adminText).toHaveValue(`${converged} offline replay`, { timeout: 20_000 });

  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2n2kAAAAASUVORK5CYII=', 'base64');
  await pasteImage(editor, 'clipboard-pixel.png', 'image/png', png);
  await expect(editor.locator('.block-image img')).toBeVisible({ timeout: 15_000 });
  await expect(editor.locator('.title-block')).toContainText('synced');
  await editor.reload();
  await expect(editor.locator('.block-image img')).toBeVisible({ timeout: 20_000 });

  const viewerContext = await contextWithoutRandomUUID(browser);
  const viewer = await viewerContext.newPage();
  viewer.on('pageerror', (error) => pageErrors.push(error));
  await login(viewer, viewerEmail, viewerInitial, 'Viewer-private-password-2026!');
  await viewer.goto(boardURL);
  await expect(viewer.locator('.title-block')).toContainText('synced');
  await expect(viewer.getByTitle('Text')).toBeDisabled();
  await expect(viewer.locator('.block-text textarea').first()).toHaveAttribute('readonly', '');
  await expect(viewer.locator('.block-image img')).toBeVisible();
  expect(pageErrors).toEqual([]);

  await viewerContext.close();
  await editorContext.close();
  await adminContext.close();
});

async function contextWithoutRandomUUID(browser: Browser) {
  const context = await browser.newContext({ locale: 'en-US' });
  await context.addInitScript(() => {
    Object.defineProperty(globalThis.crypto, 'randomUUID', { value: undefined, configurable: true });
  });
  return context;
}

async function login(page: Page, email: string, password: string, replacement: string) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  try {
    await page.waitForURL(/\/(change-password|projects)/, { timeout: 8_000 });
  } catch {
    // A retried bootstrap test may encounter the password changed by its first run.
    await page.getByLabel('Password').fill(replacement);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await page.waitForURL(/\/(change-password|projects)/);
  }
  if (page.url().includes('/change-password')) {
    await page.getByLabel('Current password').fill(password);
    await page.getByLabel('New password', { exact: true }).fill(replacement);
    await page.getByLabel('Confirm password').fill(replacement);
    await page.getByRole('button', { name: 'Save password' }).click();
  }
  await expect(page).toHaveURL(/\/projects/);
}

async function createUser(page: Page, email: string, name: string, password: string) {
  const form = page.locator('.admin-user-form');
  await form.getByLabel('Email', { exact: true }).fill(email);
  await form.getByLabel('Display name', { exact: true }).fill(name);
  await form.getByLabel('One-time password', { exact: true }).fill(password);
  await form.getByRole('button', { name: 'Create user' }).click();
  await expect(page.locator('.admin-user-row', { hasText: email })).toBeVisible();
}

async function addMember(page: Page, email: string, role: 'editor' | 'viewer', search = email, useKeyboard = false) {
  const form = page.locator('.member-form');
  const userSearch = form.getByRole('combobox', { name: 'Search users by name or email' });
  await userSearch.fill(search);
  if (useKeyboard) {
    await userSearch.press('ArrowDown');
    await userSearch.press('Enter');
  } else {
    await form.getByRole('option', { name: new RegExp(email) }).click();
  }
  await form.getByRole('combobox', { name: 'New member role' }).selectOption(role);
  await form.getByRole('button', { name: 'Add' }).click();
  await expect(page.locator('.member-row', { hasText: email }).locator('.member-role')).toHaveValue(role);
}

async function expectNoPageOverflow(page: Page) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}

async function pasteImage(page: Page, name: string, type: string, bytes: Buffer) {
  await page.evaluate(({ name, type, base64 }) => {
    const binary = atob(base64);
    const data = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    const transfer = new DataTransfer();
    transfer.items.add(new File([data], name, { type }));
    window.dispatchEvent(new ClipboardEvent('paste', {
      bubbles: true,
      cancelable: true,
      clipboardData: transfer
    }));
  }, { name, type, base64: bytes.toString('base64') });
}

async function compose(...args: string[]) {
  await execFileAsync('docker', ['compose', '-f', '../deploy/docker-compose.yml', '--env-file', '../deploy/.env.example', ...args], {
    cwd: process.cwd()
  });
}
