/**
 * testline.js
 * 
 * Test program for line mode
 */

const { DirectKeyboardHandler } = require('./handler');

async function main() {
    const handler = new DirectKeyboardHandler({
        outputStream: process.stdout,  // Enable echo
    });

    await handler.start();

    console.log('Line mode test - type lines and press Enter');
    console.log('Press Ctrl+C to exit\n');

    while (true) {
        process.stdout.write('> ');
        const line = await handler.getLine();
        
        if (line === '') {
            // Ctrl+C produces empty line
            console.log('Exiting...');
            handler.stop();
            break;
        }
        
        console.log(`You typed: ${JSON.stringify(line)}`);
    }
}

main().catch((err) => {
    console.error('Error:', err);
    process.exit(1);
});

