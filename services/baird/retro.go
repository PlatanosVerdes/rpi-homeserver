package main

// Early-2000s skin: Windows 2000 grey, title-bar gradients, bevelled buttons, Verdana at 12px; the
// left column reads as an Explorer tree.
const retroCSS = `
  :root { --paper:#d4d0c8; --panel:#ffffff; --ink:#000000; --muted:#404040; --line:#808080;
          --on:#0a246a; --on-ink:#ffffff; --on-soft:#a6caf0; --warn:#cc0000;
          --body:Verdana, Tahoma, Geneva, sans-serif; --head:Tahoma, Verdana, sans-serif; color-scheme:light; }
  body { font-size:12px; line-height:1.4; background:#3a6ea5; }
  .wrap { background:var(--paper); max-width:1000px; margin:12px auto; padding:0 0 12px;
          border:2px outset #ffffff; box-shadow:4px 4px 0 #1c3a5c; }
  .wrap::after { content:"Best viewed at 1024x768 with Internet Explorer 6 or Mozilla Firefox. Baird, made at home.";
                 display:block; text-align:center; font-size:10px; color:#404040; margin-top:16px;
                 padding-top:8px; border-top:1px solid #808080; }
  :focus-visible { outline:1px dotted #000; outline-offset:1px; }
  a { color:#0000cc; }

  header { background:linear-gradient(to right, #0a246a, #a6caf0); color:#fff; border:0; padding:6px 10px;
           align-items:center; margin-bottom:10px; }
  h1 { font:bold 20px Tahoma, sans-serif; letter-spacing:0; text-shadow:1px 1px 0 #000; }
  h1 img { width:24px; height:24px; }
  #summary { color:#fff; font-size:11px; margin:2px 0 0; }
  .look { color:#fff; font-size:11px; }
  .look nav { border:0; border-radius:0; gap:2px; background:none; }
  .look a { background:#d4d0c8; color:#000; border:2px outset #fff; padding:1px 8px; font:11px Tahoma, sans-serif; }
  .look a[aria-current] { background:#0a246a; color:#fff; border:2px inset #fff; }

  .layout { gap:10px; padding:0 10px; }
  @media (min-width:52.01rem) { .layout { grid-template-columns:250px minmax(0, 1fr); } }
  @media (max-width:52rem) {
    .wrap { margin:0; border:0; box-shadow:none; }
    .layout { padding:0 6px; }
    #back { color:#0000cc; text-decoration:underline; font:bold 11px Tahoma, sans-serif; }
  }
  .layout > nav { background:#fff; border:2px inset #fff; padding:4px; top:6px; }
  .navsec { margin-top:8px; }
  .navhead { background:linear-gradient(to right, #0a246a, #3a6ea5); color:#fff; border:0; padding:2px 5px; margin-bottom:2px; }
  .navhead b { font:bold 11px Tahoma, sans-serif; }
  .navhead span, .navhead .more { color:#fff; font-size:10px; }
  .navhead .more { text-decoration:underline; font-weight:bold; }
  .item { border-radius:0; padding:0 2px; gap:4px; }
  .item:hover { background:#eef2fa; }
  .item.sel, .item.sel:hover { background:#0a246a; }
  .item.sel .pick, .item.sel .pick small, .item.sel .new { color:#fff; }
  .item .pick { font:11px Tahoma, sans-serif; padding:1px 0; color:#0000cc; text-decoration:underline; }
  .item .pick small { font-size:10px; text-decoration:none; color:#404040; }
  .item .new { color:#cc0000; font-size:10px; font-style:normal; }
  .viewbtn, .btn, .chip { background:#d4d0c8; color:#000; border:2px outset #ffffff; border-radius:0;
                          font:11px Tahoma, sans-serif; transition:none; }
  .viewbtn.sel { background:#0a246a; color:#fff; border:2px inset #fff; }
  .viewbtn:active, .btn:active, .chip:active { border-style:inset; }
  .btn { padding:3px 10px; }
  .btn:disabled { color:#808080; text-shadow:1px 1px 0 #fff; opacity:1; }
  .btn.quiet { background:#d4d0c8; border:2px outset #fff; font-weight:normal; }
  .more { color:#0000cc; text-decoration:underline; font:11px Verdana, sans-serif; padding:2px 0; }
  .note { font-size:10px; padding:2px 4px; }
  input[type=url], input[type=text], input[type=search] { background:#fff; color:#000; border:2px inset #fff;
         border-radius:0; padding:2px 4px; font:11px Verdana, sans-serif; }

  .switch { width:13px; height:13px; border-radius:0; background:#fff; border:2px inset #fff; transition:none; }
  .switch::after { content:none; }
  .switch[aria-checked="true"], .switch[aria-checked="mixed"] { background:#fff; }
  .switch[aria-checked="true"]::before { content:"\2713"; position:absolute; inset:-3px 0 0 0; font:bold 11px Tahoma, sans-serif;
                                         color:#000; text-align:center; }
  .switch[aria-checked="mixed"]::before { content:""; position:absolute; inset:3px; background:#808080; }

  main { background:#fff; border:2px inset #fff; padding:0 0 6px; }
  #q { margin:6px 6px 0; width:calc(100% - 12px); }
  #bar { background:#ece9d8; border-top:1px solid #808080; border-bottom:1px solid #808080; padding:4px 6px; font-size:11px; margin-top:6px; }
  #fchips .label .kind { display:inline; padding:0; font:10px Tahoma, sans-serif; }
  main h2 { font:bold 12px Tahoma, sans-serif; color:#fff; background:linear-gradient(to right, #0a246a, #3a6ea5); padding:3px 6px; }
  .kind { display:block; font-size:10px; color:#404040; padding:3px 6px 0; }
  .facts { font-size:11px; color:#000; padding:0 6px; margin:4px 0 0; }
  .actions, .label, .chips { padding:0 6px; }
  .actions { margin:8px 0 4px; }
  .label { font:bold 11px Tahoma, sans-serif; margin:8px 0 3px; }
  .chip { padding:1px 6px; }
  .chip small { color:#404040; }
  .chip[aria-pressed="true"] { background:#0a246a; color:#fff; border:2px inset #fff; }
  .chip[aria-pressed="true"] small { color:#a6caf0; opacity:1; }

  #list { margin-top:8px; border-top:1px solid #808080; }
  .row { padding:3px 6px; border-bottom:1px solid #e0e0e0; gap:6px; }
  .row:nth-child(even) { background:#eef2fa; }
  .row:hover { background:#ffffcc; }
  .row.preview, .row.preview:nth-child(even) { background:#ffffe1; }
  .row img, .row .mono { width:22px; height:22px; border-radius:0; }
  .row .mono { background:#d4d0c8; color:#000; border:1px outset #fff; font:bold 9px Tahoma, sans-serif; }
  .row .name { font:bold 11px Verdana, sans-serif; color:#000; }
  .row .sub { font-size:10px; color:#404040; gap:8px; }
  .row .state { color:#0a246a; }
  .row.out .name { color:#808080; }

  dialog { background:#d4d0c8; color:#000; border:2px outset #fff; border-radius:0; padding:0; box-shadow:3px 3px 0 #000; }
  dialog::backdrop { background:rgba(0,0,0,.25); }
  dialog form { padding:0 0 8px; }
  dialog h3 { font:bold 11px Tahoma, sans-serif; color:#fff; background:linear-gradient(to right, #0a246a, #a6caf0);
              padding:3px 6px; margin:0 0 8px; }
  dialog p, dialog label, dialog .buttons { margin-left:10px; margin-right:10px; }
  dialog p { color:#000; font-size:11px; }
  dialog label { font:11px Tahoma, sans-serif; margin-top:6px; }
  dialog input { width:calc(100% - 20px); margin:0 10px; }
  #formerr { color:#cc0000; }

  #toast { background:#ffffe1; color:#000; border:1px solid #000; border-radius:0; font:11px Tahoma, sans-serif;
           box-shadow:2px 2px 0 #808080; }
  #empty { color:#404040; }
`
