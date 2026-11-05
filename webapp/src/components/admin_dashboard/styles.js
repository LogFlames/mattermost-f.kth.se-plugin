export default `
.fkth-admin {grid-area: center; min-height: 0; overflow: auto; padding: 32px; color: var(--center-channel-color, #283449); background: var(--center-channel-bg, #fff); font-size: 14px; line-height: 1.5;}
.fkth-admin * {box-sizing: border-box;}
.fkth-admin .admin-content {max-width: 1200px; margin: 0 auto;}
.fkth-admin .admin-header, .fkth-admin .admin-preview-header, .fkth-admin .admin-footer, .fkth-admin .admin-pagination {display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap;}
.fkth-admin .admin-header {margin-bottom: 28px;}
.fkth-admin h1 {font-size: 28px; line-height: 1.25; margin: 0; font-weight: 650;}
.fkth-admin h2 {font-size: 18px; margin: 0; font-weight: 600;}
.fkth-admin p {margin: 8px 0 16px;}
.fkth-admin .admin-muted, .fkth-admin small {color: rgba(var(--center-channel-color-rgb, 40,52,73), .7);}
.fkth-admin .admin-badge {font-size: 12px; padding: 5px 10px; border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .18); border-radius: 20px; white-space: nowrap;}
.fkth-admin .admin-layout {display: grid; grid-template-columns: 230px minmax(0, 1fr); gap: 28px; align-items: start;}
.fkth-admin nav {display: grid; gap: 6px;}
.fkth-admin button, .fkth-admin input, .fkth-admin select {font: inherit;}
.fkth-admin button {border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .22); border-radius: 4px; background: var(--center-channel-bg, #fff); color: inherit; padding: 8px 14px; min-height: 40px; cursor: pointer;}
.fkth-admin nav button {text-align: left; border-color: transparent; padding: 12px;}
.fkth-admin nav button span {display: block; font-weight: 600;}
.fkth-admin nav button small {display: block; font-size: 11px; margin-top: 4px; overflow-wrap: anywhere;}
.fkth-admin nav button[aria-pressed=true] {background: rgba(var(--center-channel-color-rgb, 40,52,73), .06); border-color: rgba(var(--center-channel-color-rgb, 40,52,73), .2);}
.fkth-admin .admin-workspace {min-width: 0;}
.fkth-admin .admin-panel {border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .18); border-radius: 8px; padding: 24px; margin-bottom: 24px;}
.fkth-admin fieldset {border: 0; padding: 0; margin: 0; min-width: 0;}
.fkth-admin .admin-fields {display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; margin-top: 24px;}
.fkth-admin label {display: block; font-size: 13px; font-weight: 600;}
.fkth-admin select, .fkth-admin input:not([type=checkbox]) {display: block; width: 100%; min-width: 0; min-height: 40px; margin-top: 7px; padding: 8px 10px; border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .28); border-radius: 4px; color: inherit; background: var(--center-channel-bg, #fff); font-weight: 400;}
.fkth-admin .admin-footer {border-top: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .14); margin-top: 24px; padding-top: 18px; font-size: 12px;}
.fkth-admin .admin-primary {border-color: transparent; color: var(--button-color, #fff); background: var(--button-bg, #1c58bd); font-weight: 600;}
.fkth-admin button:disabled {color: rgba(var(--center-channel-color-rgb, 40,52,73), .65); background: rgba(var(--center-channel-color-rgb, 40,52,73), .06); border-color: rgba(var(--center-channel-color-rgb, 40,52,73), .16); cursor: not-allowed;}
.fkth-admin :focus-visible {outline: 2px solid var(--link-color, #1c58bd); outline-offset: 3px;}
.fkth-admin .admin-notice, .fkth-admin .admin-error {padding: 14px 18px; border-radius: 4px; background: rgba(var(--center-channel-color-rgb, 40,52,73), .06);}
.fkth-admin ul.admin-notice {padding-left: 32px;}
.fkth-admin .admin-notice li + li {margin-top: 8px;}
.fkth-admin .admin-error {background: rgba(210,70,40,.1);}
.fkth-admin .admin-table-wrap {margin-top: 18px;}
.fkth-admin table {width: 100%; border-collapse: collapse; table-layout: fixed; font-size: 13px;}
.fkth-admin th {text-align: left; font-weight: 600; padding: 10px 8px; border-bottom: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .22);}
.fkth-admin td {padding: 12px 8px; border-bottom: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .1); vertical-align: top; overflow-wrap: anywhere;}
.fkth-admin td small {display: block; margin-top: 4px;}
.fkth-admin .admin-pagination {font-size: 12px; margin-top: 16px;}
.fkth-admin .admin-confirm {margin-top: 24px;}
.fkth-admin .admin-check {display: flex; align-items: flex-start; gap: 10px; margin-top: 20px; font-weight: 400;}
.fkth-admin input[type=checkbox] {margin: 4px 0 0; accent-color: var(--button-bg, #1c58bd); flex-shrink: 0;}
.fkth-admin .admin-empty {padding: 20px 0; max-width: 480px; color: rgba(var(--center-channel-color-rgb, 40,52,73), .7);}
@media(hover: hover) {.fkth-admin button:not(:disabled):hover {filter: brightness(1.12); border-color: var(--link-color, #1c58bd);}}
@media(max-width: 800px) {.fkth-admin {padding: 20px;} .fkth-admin .admin-layout {grid-template-columns: 1fr;} .fkth-admin nav {grid-template-columns: repeat(2, minmax(0, 1fr));} .fkth-admin .admin-panel {padding: 18px;}}
@media(max-width: 500px) {.fkth-admin {padding: 18px 12px;} .fkth-admin .admin-fields {grid-template-columns: 1fr;} .fkth-admin .admin-footer {align-items: stretch; flex-direction: column;} .fkth-admin .admin-preview-header {align-items: flex-start;} .fkth-admin h1 {font-size: 24px;} .fkth-admin .admin-panel {padding: 14px;} .fkth-admin .admin-pagination {gap: 8px;}}
`;
