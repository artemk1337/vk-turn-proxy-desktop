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
- Help tooltips use viewport-clamped fixed positioning and the popover layer where supported. Open on hover or focus, flip below when needed, and dismiss on Escape, scroll, resize and dialog closure.
- Regression case: the MTU tooltip extended beyond the left edge of the profile dialog and was clipped because its right edge was anchored to the help icon. Position shared tooltips through `initializeHelpTooltips` in `controls.js`, clamp the full tooltip rectangle to the viewport with a margin, and keep it clear of scrolling-container clipping. Escape dismisses the visible tooltip before closing its dialog.
- Segmented selectors share a sliding indicator and `data-value` selection. Preserve Arrow, Home and End navigation, focus and `aria-pressed`. Switches use native checkboxes with the `switch` role.
- Plain checkboxes share the `controls.css` style: 18px square, 5px corner radius, semantic input/border colors, blue accent fill and a thin white checkmark. Preserve native checkbox semantics, keyboard focus, disabled/indeterminate states, reduced motion and forced-colors support.
- GeoIP and GeoSite selection uses separate searchable multi-select dialogs with named subgroups. Show selected items on the routing page. Dialog edits apply on confirmation and are discarded on cancellation or Escape.
- GeoIP and GeoSite picker buttons share the same width and height in both languages.
- Selected GeoIP/GeoSite tags are removal buttons on the routing page. Reveal their cross on hover or keyboard focus and keep it visible on touch devices. Removal updates the profile draft until Save. Preserve tag focus through polling and move it to the next tag or picker after removal.
- `speed-test.js` owns the connection-page speed-test dialog, server selection, cancellable measurements and last result. Tests follow system routing, which is explained in the dialog. Preserve selected server and results across polling and language/theme changes. Download and upload have labeled progress bars driven by backend progress, using one linear Web Animation per stage, synchronized to backend stage start timestamps and measurement duration. Poll an open running test every 100 ms to catch stage changes promptly. Keep the animation timeline independent from polling and preserve its position on interruption. Keep completed and interrupted values and honor reduced motion. Latency shows its value only. Show stage status while measuring and omit completed-status and method captions.
- `routing.js` and `routing.css` own the routing page and catalog pickers. Preserve per-profile drafts across polling, navigation and language/theme changes. Load catalogs on demand and keep database update status separate from connection state.
- Keep motion purposeful. Reuse the shared timing and easing variables. Prefer transform and opacity for control animations, and honor `prefers-reduced-motion`.
- The large circular power button is the sole connection action and displays its localized action label below the icon. Use the dedicated orb palette for its fill, text and border, deriving all orb colors from the shared accent palette. Idle text, borders and connected fill use the exact UI accent-text color. Connected text uses the page background color for contrast in both themes. Keep its icon legible in both themes even when disabled. An outer arc rotates continuously while connecting or disconnecting, without showing phase progress. Connected transitions smoothly to a solid accent fill and contrasting icon/text. On a backend transition from connecting to connected, sweep a single clipped highlight across the button over 750 ms and fan six particles outward from the left and right edges. Skip it for an initially connected state or a hidden page, and cancel it when navigating away, hiding the window or enabling reduced motion. Showing the page never replays success. While connected, `particles.js` emits independent outward particles at 2–10 per second based on combined TX/RX bytes per second from the backend. Keep at most 16 live particles, preserve existing flights when the rate changes, and pause emission on hidden pages, hidden windows and reduced motion. Keep the ring, button, icon and text steady. Reduced motion hides all particles. Keep particles decorative and pointer-transparent. All motion honors reduced motion.
- Keep circular theme reveal in `theme.js` through View Transitions. Reveal from the activated control with a sharp circular boundary. Preserve capability detection, reduced-motion fallback and cancellation of an earlier transition during rapid changes.
- Complete page/dialog entrance animations before capturing a theme transition so snapshots contain fully readable content.

## State and WebView behavior

- The About page links to this repository on GitHub through the external browser, with a regular link fallback for the browser demo.
- App update status comes from `GetState().updates`. Settings provides a manual check and a native download action with save-location selection, progress and the saved path. Keep the available-version sidebar action visible across navigation, language/theme changes and polling. Both update actions use `DownloadUpdate` through the Wails bridge. Disable them during save-location selection and download, preserve status across polling and show localized download failures.

- Show cached-allocation cooldowns as quota waiting, with an explicit previous-session reason and a minute/second countdown. After TURN 486, show quota waiting with the countdown to the next one-minute availability check. For other connection retries, show the localized server error and next-attempt countdown in the connection hint. Keep the power button available to cancel, and clear the retry presentation on recovery.
- The footer shows download and upload rates beside the connection status only while connected. Use arrows and localized Mbps units with two decimal places, refresh through the one-second state polling, and hide rates during retries and disconnect. Keep metric labels accessible and profile names ellipsized.
- Update theme, language and backend state in place. Preserve editor drafts across mode changes, keyboard focus, active connections and log selection/viewport.
- Use the early theme bootstrap to match the saved theme before paint. Local storage caches the presentation theme only.
- Dropdowns render application-owned combobox/listbox controls over hidden native selects, preserving their values, change events and form reset behavior. Refresh them after programmatic value/options/disabled changes. Preserve arrows, Home/End, type-ahead, Enter/Space, Escape and Tab behavior.
- Single-selection lists share `initializeDropdowns(document)` and the `.dropdown-*` styles. Declare new fields as labeled selects and reuse the component for their interaction and popup appearance. GeoIP/GeoSite multi-selection uses the dedicated searchable dialogs.
- Use the dropdown's HTML popover layer when available and viewport-aware placement to keep menus visible inside dialogs and scrollable pages. Verify leading icons, focus restoration, disabled options and outside-click dismissal in native WebView.
- Preserve consistent field spacing and aligned inputs when adding helper controls or removing fields.
- Settings rows share the same vertical padding across group boundaries. Adjacent groups have zero extra margin and one separator, supplied by the previous row's bottom border.
- Preserve whitespace inside `#log-lines` only, keeping the log container's HTML indentation out of its rendered content. Empty logs show a top-aligned hint and hide clear/follow actions.
- Global messages use `notifications.js` with duplicate counts and a 10-second lifetime. Show remaining seconds inside a shrinking ring, switching to the dismiss cross on notification hover or focus while pausing the countdown. Repeated messages reset their lifetime. Reduced motion uses stepwise ring progress. Form validation stays beside its fields.
- Draw the notification dismiss cross with two identical centered CSS strokes so its alignment is independent of font glyph metrics.

## Verification

- Check both languages and themes at the default 1000×740 and minimum 760×560 window sizes. Exercise profile editing, hover/focus tooltips and keyboard selection.
- For tooltip changes, verify MTU and TURN help near both window edges and after scrolling the profile dialog at 760×560 and 1000×740, in both languages and themes. Check hover and keyboard focus, full text visibility, above/below placement, and Escape keeping the editor open.
- Check theme persistence, rapid theme changes, unsupported View Transitions and reduced motion. Verify that theme/language changes preserve draft values and connection state.
- Verify actual Cyrillic rendering with bundled Inter. Inspect native WebView controls as well as the browser preview, especially selects, popups and focus outlines.
- Capture screenshots after entrance animations settle. Inspect clipping, overlap, spacing and contrast, and check the console for runtime errors.
- Rebuild the embedded frontend before relaunching the local Wails preview. Native `--smoke-test` verifies bridge readiness in isolated storage. Keep temporary QA scripts and captures outside the repository.

- The profile editor exposes TURN connection selection for both profile formats. An empty selection preserves the imported transport. TCP and UDP override it on the next connection through `-turn-transport`.

- The profile editor defaults to 30 TURN connections and uses the bounds from the shared desktop settings contract. Pass the saved count through `-conns`, including the macOS helper.

- The profile editor exposes tunnel MTU for both profile formats, defaulting to 1280 with bounds 1000–1400 from shared desktop settings. The saved value overrides imported MTU through `-mtu` on the next connection.

- Settings expose global SRTP, DTLS and WRAP-A handshake timeouts as three compact second-valued fields in one row. Persist them in preferences and apply them on the next connection, preserving focused drafts during polling. Bounds and defaults come from the shared desktop settings contract.
