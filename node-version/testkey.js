const { DirectKeyboardHandler } = require('./direct-keyboard-handler');

keyboardHandler = new DirectKeyboardHandler(function(message) {
  console.error(message);
});

async function main() {
  let key = '';
  let z = 0;
  while ((key != '^C') && (z < 30)) {
    key = await keyboardHandler.getKey();
    z++;
    console.log(key);
  }
  process.exit(1);
};

main();
