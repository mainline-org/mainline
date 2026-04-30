(function(){
  var index = null;
  var input = document.getElementById('hub-search');
  var results = document.getElementById('hub-search-results');
  if (!input || !results) return;

  // Resolve root path from the stylesheet link (which already has the correct prefix).
  var rootPath = '';
  var link = document.querySelector('link[rel="stylesheet"]');
  if (link) {
    var href = link.getAttribute('href');
    rootPath = href.replace('assets/style.css', '');
  }

  function loadIndex(cb) {
    if (index) return cb(index);
    var xhr = new XMLHttpRequest();
    xhr.open('GET', rootPath + 'data/search_index.json');
    xhr.onload = function() {
      if (xhr.status === 200) {
        index = JSON.parse(xhr.responseText);
        cb(index);
      }
    };
    xhr.send();
  }

  function search(q, entries) {
    var terms = q.toLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length) return [];
    var scored = [];
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i];
      var haystack = (e.title + ' ' + e.text).toLowerCase();
      var hits = 0;
      for (var j = 0; j < terms.length; j++) {
        if (haystack.indexOf(terms[j]) >= 0) hits++;
      }
      if (hits === terms.length) {
        scored.push({ entry: e, score: hits });
      }
    }
    scored.sort(function(a, b) { return b.score - a.score; });
    return scored.slice(0, 20);
  }

  function render(matches) {
    if (!matches.length) {
      results.innerHTML = '<div class="search-empty">No results found.</div>';
      results.style.display = 'block';
      return;
    }
    var groups = {};
    var order = ['intent', 'file', 'constraint'];
    for (var i = 0; i < matches.length; i++) {
      var t = matches[i].entry.type;
      if (!groups[t]) groups[t] = [];
      groups[t].push(matches[i].entry);
    }
    var html = '';
    for (var k = 0; k < order.length; k++) {
      var key = order[k];
      if (!groups[key]) continue;
      var label = key === 'intent' ? 'Intents' : key === 'file' ? 'Files' : 'Constraints';
      html += '<div class="search-group"><strong>' + label + '</strong>';
      for (var j = 0; j < groups[key].length; j++) {
        var e = groups[key][j];
        // Resolve relative URL from current page to root, then to entry
        var url = rootPath + e.url;
        html += '<a class="search-hit" href="' + url + '">' + escHtml(e.title) + '</a>';
      }
      html += '</div>';
    }
    results.innerHTML = html;
    results.style.display = 'block';
  }

  function escHtml(s) {
    var d = document.createElement('span');
    d.textContent = s;
    return d.innerHTML;
  }

  var timer = null;
  input.addEventListener('input', function() {
    clearTimeout(timer);
    var q = input.value.trim();
    if (!q) { results.style.display = 'none'; return; }
    timer = setTimeout(function() {
      loadIndex(function(idx) { render(search(q, idx)); });
    }, 150);
  });

  document.addEventListener('click', function(e) {
    if (!results.contains(e.target) && e.target !== input) {
      results.style.display = 'none';
    }
  });

  // Copy-to-clipboard for CLI command blocks.
  document.addEventListener('click', function(e) {
    var pre = e.target.closest('section.commands pre');
    if (!pre) return;
    var text = pre.textContent;
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text);
      pre.classList.add('copied');
      setTimeout(function() { pre.classList.remove('copied'); }, 1200);
    }
  });
})();
