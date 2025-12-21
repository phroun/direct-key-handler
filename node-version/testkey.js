/**
 * testkey.js
 * 
 * Test program for the keyboard handler
 */

const { DirectKeyboardHandler } = require('./handler');

async function main() {
    const handler = new DirectKeyboardHandler({
        // debugFn: (msg) => console.error(`[DEBUG] ${msg}`),
    });

    await handler.start();

    console.log('Keyboard test - press keys to see their names');
    console.log('Press Ctrl+C to exit\n');

    // Using callback style
    handler.onKey((key) => {
        console.log(`Key: ${JSON.stringify(key)}`);
        
        if (key === '^C') {
            console.log('\nExiting...');
            handler.stop();
            process.exit(0);
        }
    });

    // Or using async/await style:
    // while (true) {
    //     const key = await handler.getKey();
    //     console.log(`Key: ${JSON.stringify(key)}`);
    //     if (key === '^C') {
    //         handler.stop();
    //         break;
    //     }
    // }
}

main().catch((err) => {
    console.error('Error:', err);
    process.exit(1);
});
