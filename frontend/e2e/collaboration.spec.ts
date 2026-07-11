import { expect, test, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

const bootstrapEmail = process.env.E2E_ADMIN_EMAIL ?? 'admin@example.com';
const bootstrapPassword = process.env.E2E_ADMIN_PASSWORD ?? 'replace-with-a-unique-12+-character-password';

test('admin setup, dual-session collaboration, reconnect, viewer permissions, and image persistence', async ({ browser }) => {
  test.setTimeout(120_000);
  const suffix = Date.now().toString(36);
  const editorEmail = `editor-${suffix}@example.com`;
  const viewerEmail = `viewer-${suffix}@example.com`;
  const editorInitial = 'Editor-one-time-2026!';
  const viewerInitial = 'Viewer-one-time-2026!';
  const projectName = `E2E Project ${suffix}`;
  const boardName = `Collaboration ${suffix}`;

  const adminContext = await browser.newContext();
  const admin = await adminContext.newPage();
  await login(admin, bootstrapEmail, bootstrapPassword, 'Admin-private-password-2026!');

  await admin.goto('/admin');
  await createUser(admin, editorEmail, 'E2E Editor', editorInitial);
  await createUser(admin, viewerEmail, 'E2E Viewer', viewerInitial);

  await admin.goto('/projects');
  await admin.getByPlaceholder('Project name').fill(projectName);
  await admin.getByPlaceholder('Description (optional)').fill('Playwright collaboration project');
  await admin.getByRole('button', { name: 'Create' }).click();
  await expect(admin.getByRole('heading', { name: projectName })).toBeVisible();
  const projectURL = admin.url();

  await admin.getByPlaceholder('Board name').fill(boardName);
  await admin.getByRole('button', { name: 'Board' }).click();
  await expect(admin.locator('.board-open', { hasText: boardName })).toBeVisible();

  await addMember(admin, editorEmail, 'editor');
  await addMember(admin, viewerEmail, 'viewer');

  await admin.locator('.board-open', { hasText: boardName }).click();
  await expect(admin).toHaveURL(/\/boards\//);
  const boardURL = admin.url();
  await expect(admin.locator('.title-block')).toContainText('synced');

  const editorContext = await browser.newContext();
  const editor = await editorContext.newPage();
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
  await compose('stop', 'api');
  try {
    await expect(editor.locator('.title-block')).toContainText('offline', { timeout: 20_000 });
    await editorText.fill(`${converged} offline replay`);
  } finally {
    await compose('up', '-d', 'api');
  }
  await expect.poll(async () => fetch(`${process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080'}/readyz`).then((response) => response.status).catch(() => 0), { timeout: 30_000 }).toBe(200);
  await expect(editor.locator('.title-block')).toContainText('synced', { timeout: 20_000 });
  await expect(adminText).toHaveValue(`${converged} offline replay`, { timeout: 20_000 });

  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl2n2kAAAAASUVORK5CYII=', 'base64');
  await editor.locator('input[type="file"]').setInputFiles({ name: 'pixel.png', mimeType: 'image/png', buffer: png });
  await expect(editor.locator('.block-image img')).toBeVisible({ timeout: 15_000 });
  await expect(editor.locator('.title-block')).toContainText('synced');
  await editor.reload();
  await expect(editor.locator('.block-image img')).toBeVisible({ timeout: 20_000 });

  const viewerContext = await browser.newContext();
  const viewer = await viewerContext.newPage();
  await login(viewer, viewerEmail, viewerInitial, 'Viewer-private-password-2026!');
  await viewer.goto(boardURL);
  await expect(viewer.locator('.title-block')).toContainText('synced');
  await expect(viewer.getByTitle('Text')).toBeDisabled();
  await expect(viewer.locator('.block-text textarea').first()).toHaveAttribute('readonly', '');
  await expect(viewer.locator('.block-image img')).toBeVisible();

  await viewerContext.close();
  await editorContext.close();
  await adminContext.close();
});

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
  await page.getByPlaceholder('email').fill(email);
  await page.getByPlaceholder('name').fill(name);
  await page.getByPlaceholder('one-time password').fill(password);
  await page.getByRole('button', { name: 'Add' }).click();
  await expect(page.locator('.user-row', { hasText: email })).toBeVisible();
}

async function addMember(page: Page, email: string, role: 'editor' | 'viewer') {
  const form = page.locator('.member-form');
  await form.locator('select').nth(0).selectOption({ label: email });
  await form.locator('select').nth(1).selectOption(role);
  await form.getByRole('button', { name: 'Add' }).click();
  await expect(page.locator('.member-row', { hasText: email })).toContainText(role);
}

async function compose(...args: string[]) {
  await execFileAsync('docker', ['compose', '-f', '../deploy/docker-compose.yml', '--env-file', '../deploy/.env.example', ...args], {
    cwd: process.cwd()
  });
}
