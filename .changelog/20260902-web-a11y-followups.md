+ [web] pinned services reorder from the keyboard, by a drag handle that takes focus.
  the sidebar had a PointerSensor and nothing else, so reordering was a mouse-only feature; Space picks a row up, the arrows move it, Space drops it and Escape cancels
~ [web] the sidebar is a list, so its length and position are announced rather than only drawn.
  sections, sub-navigation and the footer tools are `<ul>`/`<li>`; dnd-kit's announcements move to the body so they are not a stray child of the list
* [web] a tab and its panel point at each other, so the panel is announced as the thing the tab opened.
  `aria-controls`, `aria-labelledby` and a focusable panel — without them a tab whose panel holds no focusable element led nowhere
* [web] the global search palette is a combobox with a listbox, and says how many results it found.
  arrowing the selection moves `aria-activedescendant` instead of changing nothing an announcer can see, and the pin button on a service chip is no longer nested inside the chip's own button, which made it mouse-only
* [web] a DynamoDB table that fails to load says so instead of rendering an empty page.
  a failed DescribeTable returned nothing at all — no heading, no message; it now follows the same error treatment as every other list in the app
* [web] virtualized tables report their real length.
  only the windowed rows are in the DOM, so without `aria-rowcount`/`aria-rowindex` a screen reader announced "row 3 of 20" inside a five-thousand-row table
