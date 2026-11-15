(function () {
  "use strict";
  var root = document.documentElement;
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  root.classList.add("js");

  var btn = document.getElementById("theme");
  var icon = document.getElementById("theme-icon");
  var mq = window.matchMedia("(prefers-color-scheme: dark)");
  function current() {
    return root.getAttribute("data-theme") || (mq.matches ? "dark" : "light");
  }
  function sync() {
    var dark = current() === "dark";
    btn.setAttribute("aria-pressed", dark ? "true" : "false");
    icon.textContent = dark ? "☾" : "☀";
  }
  btn.addEventListener("click", function () {
    var next = current() === "dark" ? "light" : "dark";
    root.setAttribute("data-theme", next);
    try { localStorage.setItem("theme", next); } catch (e) {}
    sync();
  });
  mq.addEventListener("change", sync);
  sync();

  document.querySelectorAll(".copy").forEach(function (b) {
    b.addEventListener("click", function () {
      var code = b.parentNode.querySelector("code");
      var reset = function (label) {
        b.textContent = label;
        setTimeout(function () { b.textContent = "Copy"; b.classList.remove("done"); }, 1800);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(code.innerText).then(function () {
          b.classList.add("done");
          reset("Copied");
        }, function () { reset("Select and copy"); });
      } else {
        var r = document.createRange();
        r.selectNodeContents(code);
        var s = window.getSelection();
        s.removeAllRanges();
        s.addRange(r);
        reset("Press Ctrl+C");
      }
    });
  });

  var els = document.querySelectorAll(".reveal");
  if (reduce || !("IntersectionObserver" in window)) {
    els.forEach(function (e) { e.classList.add("in"); });
  } else {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting) { en.target.classList.add("in"); io.unobserve(en.target); }
      });
    }, { threshold: 0.12 });
    els.forEach(function (e) { io.observe(e); });
  }

  var demo = document.getElementById("demo");
  if (!demo || reduce) return;
  var tokens = demo.innerHTML.match(/<[^>]+>|&[a-z]+;|[\s\S]/g);
  var out = "";
  var open = 0;
  var i = 0;
  demo.textContent = "";
  demo.classList.add("typing");

  function step() {
    if (i >= tokens.length) { demo.classList.remove("typing"); return; }
    var t = tokens[i++];
    out += t;
    if (t.indexOf("<span") === 0) open++;
    else if (t === "</span>") open--;
    demo.innerHTML = out + new Array(open + 1).join("</span>");
    var delay = 14;
    if (t === "\n") delay = /\$ lobster [^\n]*\n$/.test(out) ? 450 : 80;
    setTimeout(step, delay);
  }
  setTimeout(step, 600);
})();
