function initialize() {
  document.body.innerText = 'so far so good';
  register();
}

function open_channel(token) {
  var channel = new goog.appengine.Channel(token);
  var handler = {
    'onopen': function() { console.log('channel opened...'); },
    'onmessage': function(m) { console.log('message: ', m); },
    'onerror': function(e) { console.log('error: ', e); },
    'onclose': function() { console.log('channel closed'); }
  };

  channel.open(handler);
}

function register() {
  var now = (new Date()).getTime();  // UNIX timestamp, in milliseconds
  xhr_post_json('/register?clientId=' + encodeURIComponent('ch-' + now));
}

function xhr_post_json(url, obj) {
  var xhr = new XMLHttpRequest();
  xhr.open("POST", url, true);

  xhr.onreadystatechange = function() {
    if (xhr.readyState == 4) {
      if (xhr.status == 200) {
        console.log('yay!');
        var rsp = JSON.parse(xhr.responseText);
        open_channel(rsp['token']);
      } else {
        console.log('fail!');
      }
    }
  };

  if (obj) {
    xhr.setRequestHeader("Content-Type", "application/json");
    xhr.send(JSON.stringify(obj));
  } else {
    xhr.send()
  }
}
