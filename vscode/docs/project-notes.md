# Project notes

- `npm run pictures` needs Playwright's Chromium with its system libraries (`libglib-2.0.so.0` and others). The agents' sandbox lacks them and cannot install them, so retaking the README GIFs is a person's step, never an agent's.
- The side bar shortcuts (`Control+b`, `Control+Alt+b`) toggle, so a bar that starts hidden opens on a fresh machine (the secondary side bar showed an empty CHAT panel in every picture). `test/pictures/take.ts` therefore runs `workbench.action.closeSidebar` and `workbench.action.closeAuxiliaryBar` and checks that the secondary side bar is off screen before the first picture, or stops with an error.
