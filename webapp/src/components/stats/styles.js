export default `
.fkth-stats {grid-area: center; min-height: 0; overflow: auto; color: var(--center-channel-color, #283449); background: var(--center-channel-bg, #f8f9fc); padding: 32px; font-size: 14px; line-height: 1.5;}
.fkth-stats * {box-sizing: border-box;}
.fkth-stats .stats-content {max-width: 1240px; margin: 0 auto;}
.fkth-stats h1 {font-size: 28px; line-height: 1.25; margin: 4px 0 0; font-weight: 650;}
.fkth-stats h2 {font-size: 17px; margin: 0; font-weight: 600;}
.fkth-stats .stats-header, .fkth-stats .stats-results-heading, .fkth-stats .stats-form-footer {display: flex; justify-content: space-between; align-items: center; gap: 16px; flex-wrap: wrap;}
.fkth-stats .stats-eyebrow {color: var(--link-color, #1c58bd); font-size: 11px; font-weight: 700; letter-spacing: 1.5px;}
.fkth-stats .stats-muted, .fkth-stats .stats-footnote {color: rgba(var(--center-channel-color-rgb, 40,52,73), .68);}
.fkth-stats .stats-badge {font-size: 12px; padding: 6px 10px; border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .14); border-radius: 20px;}
.fkth-stats .stats-card {background: var(--center-channel-bg, #fff); border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .15); border-radius: 8px; padding: 24px; margin: 24px 0;}
.fkth-stats fieldset {border: 0; padding: 0; margin: 0; min-width: 0;}
.fkth-stats .stats-filter-grid {display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 28px;}
.fkth-stats .stats-field {display: flex; flex-direction: column; gap: 8px; min-width: 0;}
.fkth-stats label {display: block; font-weight: 600; font-size: 13px; margin: 0;}
.fkth-stats select, .fkth-stats input:not([type=checkbox]) {width: 100%; min-width: 0; min-height: 38px; border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .25); background: var(--center-channel-bg, #fff); color: inherit; border-radius: 4px; padding: 7px 10px; font: inherit;}
.fkth-stats button, .fkth-stats input, .fkth-stats select {font: inherit;}
.fkth-stats :focus-visible {outline: 2px solid var(--link-color, #1c58bd); outline-offset: 3px;}
.fkth-stats .stats-check {display: flex; align-items: center; gap: 8px; margin-top: 5px; font-weight: 400;}
.fkth-stats input[type=checkbox] {margin: 0; accent-color: var(--button-bg, #1c58bd);}
.fkth-stats .stats-dates {display: grid; grid-template-columns: 1fr 1fr; gap: 10px;}
.fkth-stats .stats-dates label {margin-bottom: 8px;}
.fkth-stats .stats-form-footer {border-top: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .12); margin-top: 24px; padding-top: 18px; flex-wrap: nowrap; justify-content: flex-end;}
.fkth-stats .stats-show {border: 0; border-radius: 4px; background: var(--button-bg, #1c58bd); color: var(--button-color, #fff); min-height: 40px; padding: 9px 32px; font-weight: 600; cursor: pointer;}
.fkth-stats .stats-show:hover {filter: brightness(1.12);}
.fkth-stats fieldset:disabled {opacity: .6; pointer-events: none;}
.fkth-stats .stats-error, .fkth-stats .stats-notice {padding: 14px 18px; border-radius: 4px; background: rgba(210,70,40,.1);}
.fkth-stats .stats-notice {background: rgba(30,100,220,.08);}
.fkth-stats .stats-empty {padding: 44px 16px; text-align: center;}
.fkth-stats .stats-footnote {font-size: 12px; margin: 24px 0; max-width: 900px;}
.fkth-stats .stats-results-heading {margin-top: 30px;}
.fkth-stats .stats-results-heading span {font-size: 12px;}
.fkth-stats .stats-display {display: flex; flex-wrap: wrap; gap: 4px; margin: 16px 0;}
.fkth-stats .stats-display button {padding: 7px 14px; border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .25); border-radius: 4px; background: var(--center-channel-bg, #fff); color: inherit; cursor: pointer;}
.fkth-stats .stats-display button[aria-pressed=true] {background: var(--button-bg, #1c58bd); color: var(--button-color, #fff); border-color: transparent;}
.fkth-stats .stats-chart {position: relative;}
.fkth-stats .stats-chart svg {display: block; width: 100%; touch-action: pan-y; overflow: visible;}
.fkth-stats .stats-chart text {fill: currentColor; font-family: inherit; font-size: 12px;}
.fkth-stats .stats-chart-subtitle {font-size: 12px; margin: 4px 0 18px;}
.fkth-stats .stats-tooltip {position: absolute; z-index: 2; top: 12px; width: 215px; max-height: 190px; overflow-y: auto; background: var(--center-channel-bg, #fff); border: 1px solid rgba(var(--center-channel-color-rgb, 40,52,73), .2); box-shadow: 0 4px 18px rgba(0,0,0,.12); padding: 12px; border-radius: 6px; font-size: 12px;}
.fkth-stats .stats-tooltip-row {display: flex; align-items: center; gap: 7px; margin-top: 6px;}
.fkth-stats .stats-tooltip-row span:nth-child(2) {flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;}
.fkth-stats .stats-swatch {display: inline-block; flex-shrink: 0; width: 8px; height: 8px; border-radius: 50%;}
.fkth-stats .stats-legend {display: flex; gap: 6px 16px; flex-wrap: wrap; max-height: 116px; overflow-y: auto; margin: 14px 0;}
.fkth-stats .stats-legend button {background: transparent; color: inherit; border: 1px solid transparent; border-radius: 4px; padding: 6px; cursor: pointer; font-size: 12px; display: flex; gap: 7px; align-items: center;}
.fkth-stats .stats-legend button[aria-pressed=false] {opacity: .45; text-decoration: line-through;}
.fkth-stats .stats-legend button:hover {border-color: rgba(var(--center-channel-color-rgb, 40,52,73), .2);}
.fkth-stats .stats-select__control, .fkth-stats .stats-select__menu {background: var(--center-channel-bg, #fff); color: inherit; border-color: rgba(var(--center-channel-color-rgb, 40,52,73), .25);}
.fkth-stats .stats-select__menu {z-index: 5;}
.fkth-stats .stats-select__input, .fkth-stats .stats-select__single-value, .fkth-stats .stats-select__multi-value__label {color: inherit;}
.fkth-stats .stats-select__input input {min-height: 0; padding: 0;}
.fkth-stats .stats-select__multi-value {background: rgba(var(--center-channel-color-rgb, 40,52,73), .1);}
.fkth-stats .stats-select__option--is-focused {background: rgba(var(--center-channel-color-rgb, 40,52,73), .1);}
@media(max-width: 900px) {.fkth-stats .stats-filter-grid {grid-template-columns: 1fr;} .fkth-stats .stats-field {max-width: 100%;}}
@media(max-width: 600px) {.fkth-stats {padding: 18px 12px;} .fkth-stats h1 {font-size: 24px;} .fkth-stats .stats-card {padding: 16px;} .fkth-stats .stats-form-footer {align-items: stretch; flex-direction: column;} .fkth-stats .stats-badge {margin-top: 4px;}}
`;
