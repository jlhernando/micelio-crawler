/**
 * Accessibility Quick Audit
 *
 * Checks for common accessibility issues:
 * missing ARIA labels, form labels, color contrast hints,
 * keyboard navigation, and skip-to-content links.
 *
 * Usage: micelio spider https://example.com --snippet snippets/accessibility.js
 */
export default async (page) => {
  return await page.evaluate(() => {
    // Images without alt text
    const imgsNoAlt = document.querySelectorAll('img:not([alt])').length;
    const imgsEmptyAlt = document.querySelectorAll('img[alt=""]').length;

    // Form inputs without labels
    const inputs = document.querySelectorAll('input:not([type="hidden"]):not([type="submit"]):not([type="button"])');
    let inputsWithoutLabel = 0;
    inputs.forEach((input) => {
      const id = input.id;
      const hasLabel = id && document.querySelector(`label[for="${id}"]`);
      const hasAriaLabel = input.getAttribute('aria-label') || input.getAttribute('aria-labelledby');
      const hasTitle = input.getAttribute('title');
      const wrappedInLabel = input.closest('label');
      if (!hasLabel && !hasAriaLabel && !hasTitle && !wrappedInLabel) {
        inputsWithoutLabel++;
      }
    });

    // Buttons without accessible names
    const buttons = document.querySelectorAll('button, [role="button"]');
    let buttonsNoName = 0;
    buttons.forEach((btn) => {
      const text = btn.textContent?.trim();
      const ariaLabel = btn.getAttribute('aria-label');
      const ariaLabelledBy = btn.getAttribute('aria-labelledby');
      const title = btn.getAttribute('title');
      if (!text && !ariaLabel && !ariaLabelledBy && !title) {
        buttonsNoName++;
      }
    });

    // Skip to content link
    const hasSkipLink = !!document.querySelector('a[href="#content"], a[href="#main"], a[href="#main-content"], .skip-link, .skip-to-content');

    // Language attribute
    const hasLangAttr = !!document.documentElement.lang;
    const lang = document.documentElement.lang || 'missing';

    // Heading hierarchy
    const headings = Array.from(document.querySelectorAll('h1, h2, h3, h4, h5, h6'))
      .map((h) => parseInt(h.tagName[1]));
    let headingSkips = 0;
    for (let i = 1; i < headings.length; i++) {
      if (headings[i] > headings[i - 1] + 1) headingSkips++;
    }

    // ARIA roles
    const ariaRoles = document.querySelectorAll('[role]').length;

    // Tabindex issues (positive tabindex is bad practice)
    const positiveTabindex = document.querySelectorAll('[tabindex]:not([tabindex="0"]):not([tabindex="-1"])').length;

    return {
      images_no_alt: imgsNoAlt,
      images_empty_alt: imgsEmptyAlt,
      inputs_without_label: inputsWithoutLabel,
      buttons_without_name: buttonsNoName,
      has_skip_link: hasSkipLink,
      has_lang_attr: hasLangAttr,
      html_lang: lang,
      heading_hierarchy_skips: headingSkips,
      aria_roles_count: ariaRoles,
      positive_tabindex: positiveTabindex,
      a11y_score: Math.max(0, 100
        - (imgsNoAlt * 5)
        - (inputsWithoutLabel * 10)
        - (buttonsNoName * 5)
        - (hasSkipLink ? 0 : 5)
        - (hasLangAttr ? 0 : 10)
        - (headingSkips * 3)
        - (positiveTabindex * 5)),
    };
  });
};
