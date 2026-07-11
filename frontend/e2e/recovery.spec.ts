import { expect, test } from '@playwright/test';

test.skip(process.env.E2E_RECOVERY !== '1', 'runs only after the CI backup has been restored');

test('restored database, Yjs document, and upload archive are usable', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill(process.env.E2E_ADMIN_EMAIL ?? 'admin@example.com');
  await page.getByLabel('Password').fill(process.env.E2E_ADMIN_PASSWORD ?? 'Admin-private-password-2026!');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/\/projects/);

  const project = page.locator('.nav-list button', { hasText: 'E2E Project' }).first();
  await expect(project).toBeVisible();
  await project.click();
  const board = page.locator('.board-open', { hasText: 'Collaboration' }).first();
  await expect(board).toBeVisible();
  await board.click();

  await expect(page.locator('.title-block')).toContainText('synced');
  await expect(page.locator('.block-text textarea').first()).toHaveValue(/offline replay/);
  await expect(page.locator('.block-image img').first()).toBeVisible();
  await expect(page.locator('.block-image img').first()).toHaveJSProperty('naturalWidth', 1);
});
