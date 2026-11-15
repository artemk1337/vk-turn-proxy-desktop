# Desktop frontend

Follow the parent [AGENTS.md](../AGENTS.md).

## Maintaining these instructions

- Update this file in the same change whenever shared UI components, templates, design conventions, animation behavior or frontend verification workflows change. Keep the instructions aligned with the implemented behavior.
- Document shared behavior at this level and link to it from parent instructions. Update the parent AGENTS.md when responsibility boundaries or global contracts change.

## Design

- Follow the Quiet Blue design with blue accents, solid backgrounds, a left navigation rail and restrained borders and shadows. Keep the sidebar background uniform.
- Use the semantic light/dark palette in `styles.css` for surfaces, inputs, text, borders and accents. Check contrast and focus visibility in both themes.
- Use the bundled variable Inter font with Cyrillic support. Keep its license with the assets. Match existing typography, spacing and control geometry.
- `assets/logo.png` is the canonical VT logo. Regenerate Windows ICO, Linux PNG, embedded frontend and tray images with `installer/build-icons.py` after changing it. Keep the sidebar logo visible at compact widths and preserve the tray's connection-state badge.
- Keep interface text short and practical. Store English and Russian strings in `i18n.js`. Explain technical fields through the shared circled-i tooltip with `aria-describedby`, hover and keyboard focus.
- Use a title-only heading for Settings. Keep the page focused on actionable controls and their necessary hints. Explain technical details in contextual help.

## Shared controls and motion

- Reuse `controls.js`, `dropdown.js` and `controls.css` for segmented selectors, switches, dropdowns and help tooltips. Customize variants through CSS variables.
- Segmented selectors share a sliding indicator and `data-value` selection. Preserve Arrow, Home and End navigation, focus and `aria-pressed`. Switches use native checkboxes with the `switch` role.
- Keep motion purposeful. Reuse the shared timing and easing variables. Prefer transform and opacity for control animations, and honor `prefers-reduced-motion`.
- The large circular power button is the sole connection action and displays its localized action label below the icon. Use the dedicated orb palette for its fill, text and border, deriving all orb colors from the shared accent palette. Idle text, borders and connected fill use the exact UI accent-text color. Connected text uses the page background color for contrast in both themes. Keep its icon legible in both themes even when disabled. An outer arc rotates continuously while connecting or disconnecting, without showing phase progress. Connected transitions smoothly to a solid accent fill and contrasting icon/text. On a backend transition from connecting to connected, sweep a single clipped highlight across the button over 750 ms and fan six particles outward from the left and right edges. Skip it for an initially connected state or a hidden page, and cancel it when navigating away, hiding the window or enabling reduced motion. Showing the page never replays success. While connected, `particles.js` emits independent outward particles at 2–10 per second based on combined TX/RX bytes per second from the backend. Keep at most 16 live particles, preserve existing flights when the rate changes, and pause emission on hidden pages, hidden windows and reduced motion. Keep the ring, button, icon and text steady. Reduced motion hides all particles. Keep particles decorative and pointer-transparent. All motion honors reduced motion.
- Keep circular theme reveal in `theme.js` through View Transitions. Reveal from the activated control with a sharp circular boundary. Preserve capability detection, reduced-motion fallback and cancellation of an earlier transition during rapid changes.
- Complete page/dialog entrance animations before capturing a theme transition so snapshots contain fully readable content.

## State and WebView behavior

- Update theme, language and backend state in place. Preserve editor drafts across mode changes, keyboard focus, active connections and log selection/viewport.
- Use the early theme bootstrap to match the saved theme before paint. Local storage caches the presentation theme only.
- Dropdowns render application-owned combobox/listbox controls over hidden native selects, preserving their values, change events and form reset behavior. Refresh them after programmatic value/options/disabled changes. Preserve arrows, Home/End, type-ahead, Enter/Space, Escape and Tab behavior.
- All selection lists share `initializeDropdowns(document)` and the `.dropdown-*` styles. Declare new fields as labeled selects and reuse the component for their interaction and popup appearance.
- Use the dropdown's HTML popover layer when available and viewport-aware placement to keep menus visible inside dialogs and scrollable pages. Verify leading icons, focus restoration, disabled options and outside-click dismissal in native WebView.
- Preserve consistent field spacing and aligned inputs when adding helper controls or removing fields.
- Settings rows share the same vertical padding across group boundaries. Adjacent groups have zero extra margin and one separator, supplied by the previous row's bottom border.
- Preserve whitespace inside `#log-lines` only, keeping the log container's HTML indentation out of its rendered content. Empty logs show a top-aligned hint and hide clear/follow actions.
- Global messages use `notifications.js` with duplicate counts and a 10-second lifetime. Show remaining seconds inside a shrinking ring, switching to the dismiss cross on notification hover or focus while pausing the countdown. Repeated messages reset their lifetime. Reduced motion uses stepwise ring progress. Form validation stays beside its fields.
- Draw the notification dismiss cross with two identical centered CSS strokes so its alignment is independent of font glyph metrics.

## Verification

- Check both languages and themes at the default 1000×740 and minimum 760×560 window sizes. Exercise profile editing, hover/focus tooltips and keyboard selection.
- Check theme persistence, rapid theme changes, unsupported View Transitions and reduced motion. Verify that theme/language changes preserve draft values and connection state.
- Verify actual Cyrillic rendering with bundled Inter. Inspect native WebView controls as well as the browser preview, especially selects, popups and focus outlines.
- Capture screenshots after entrance animations settle. Inspect clipping, overlap, spacing and contrast, and check the console for runtime errors.
- Rebuild the embedded frontend before relaunching the local Wails preview. Native `--smoke-test` verifies bridge readiness in isolated storage. Keep temporary QA scripts and captures outside the repository.
