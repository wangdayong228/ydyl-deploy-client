async function loopPrint(count) {
    for (let i = 0; i < count; i++) {
        await new Promise(resolve => setTimeout(resolve, 1000));
        console.log(`${i} ${new Date().toISOString()}`);
    }
}

loopPrint(100).then(() => {
    console.log("done");
});