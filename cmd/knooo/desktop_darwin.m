//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static void stopEventLoop(void) {
    [NSApp stop:nil];
    [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined
        location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil
        subtype:0 data1:0 data2:0] atStart:NO];
}

void knoooStop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ stopEventLoop(); });
}

void knoooError(const char *message) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        NSAlert *alert = [[NSAlert alloc] init];
        alert.messageText = @"Knooo";
        alert.informativeText = [NSString stringWithUTF8String:message];
        [alert runModal];
    }
}

@interface KnoooWindow : NSObject <NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate>
@property (strong) NSWindow *window;
@property (strong) NSURL *startURL;
@property (strong) NSMapTable<WKDownload *, NSURL *> *exports;
@end

@implementation KnoooWindow
- (void)windowWillClose:(NSNotification *)notification { stopEventLoop(); }

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    stopEventLoop();
    return NSTerminateCancel; // Return through Go so the HTTP server and database close.
}

- (void)webView:(WKWebView *)view decidePolicyForNavigationAction:(WKNavigationAction *)action
    decisionHandler:(void (^)(WKNavigationActionPolicy))decide {
    NSURL *url = action.request.URL;
    BOOL local = [url.scheme isEqual:self.startURL.scheme] && [url.host isEqual:self.startURL.host]
        && [url.port isEqual:self.startURL.port];
    if (action.shouldPerformDownload && (local || [url.scheme isEqual:@"blob"])) {
        decide(WKNavigationActionPolicyDownload);
    } else if (local && action.targetFrame != nil) {
        decide(WKNavigationActionPolicyAllow);
    } else {
        if (action.navigationType == WKNavigationTypeLinkActivated &&
            ([@[@"http", @"https", @"mailto"] containsObject:url.scheme])) {
            [[NSWorkspace sharedWorkspace] openURL:url];
        }
        decide(WKNavigationActionPolicyCancel);
    }
}

- (void)webView:(WKWebView *)view navigationAction:(WKNavigationAction *)action didBecomeDownload:(WKDownload *)download {
    download.delegate = self;
}

- (void)download:(WKDownload *)download decideDestinationUsingResponse:(NSURLResponse *)response
    suggestedFilename:(NSString *)name completionHandler:(void (^)(NSURL *))complete {
    // WebKit requires a new file. Stage it before offering to replace any user file.
    NSURL *dir = [NSURL fileURLWithPath:NSTemporaryDirectory() isDirectory:YES];
    NSString *filename = name.lastPathComponent;
    if (!filename.length || [@[@".", @"..", @"/"] containsObject:filename]) filename = @"Knooo";
    NSURL *stage = [[dir URLByAppendingPathComponent:NSUUID.UUID.UUIDString] URLByAppendingPathComponent:filename];
    NSError *error = nil;
    if (![[NSFileManager defaultManager] createDirectoryAtURL:stage.URLByDeletingLastPathComponent
            withIntermediateDirectories:NO attributes:@{NSFilePosixPermissions: @0700} error:&error]) {
        [NSApp presentError:error];
        complete(nil);
        return;
    }
    [self.exports setObject:stage forKey:download];
    complete(stage);
}

- (void)cleanExport:(WKDownload *)download {
    NSURL *stage = [self.exports objectForKey:download];
    if (stage) {
        [[NSFileManager defaultManager] removeItemAtURL:stage.URLByDeletingLastPathComponent error:nil];
        [self.exports removeObjectForKey:download];
    }
}

- (void)downloadDidFinish:(WKDownload *)download {
    NSURL *stage = [self.exports objectForKey:download];
    NSSavePanel *panel = [NSSavePanel savePanel];
    panel.nameFieldStringValue = stage.lastPathComponent;
    [panel beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse choice) {
        if (choice == NSModalResponseOK) {
            NSError *error = nil;
            NSData *data = [NSData dataWithContentsOfURL:stage options:NSDataReadingMappedIfSafe error:&error];
            if (!data || ![data writeToURL:panel.URL options:NSDataWritingAtomic error:&error]) {
                [NSApp presentError:error];
            }
        }
        [self cleanExport:download];
    }];
}

- (void)download:(WKDownload *)download didFailWithError:(NSError *)error resumeData:(NSData *)resumeData {
    [self cleanExport:download];
    [NSApp presentError:error];
}

- (void)webView:(WKWebView *)view runOpenPanelWithParameters:(WKOpenPanelParameters *)parameters
    initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(NSArray<NSURL *> *))complete {
    NSOpenPanel *panel = [NSOpenPanel openPanel];
    panel.allowsMultipleSelection = parameters.allowsMultipleSelection;
    [panel beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse choice) {
        complete(choice == NSModalResponseOK ? panel.URLs : nil);
    }];
}

- (void)webView:(WKWebView *)view runJavaScriptConfirmPanelWithMessage:(NSString *)message
    initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))complete {
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = message;
    [alert addButtonWithTitle:@"确定"];
    [alert addButtonWithTitle:@"取消"];
    [alert beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse choice) {
        complete(choice == NSAlertFirstButtonReturn);
    }];
}
@end

void knoooRun(const char *address) {
    @autoreleasepool {
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        __attribute__((objc_precise_lifetime)) KnoooWindow *host = [[KnoooWindow alloc] init];
        host.startURL = [NSURL URLWithString:[NSString stringWithUTF8String:address]];
        host.exports = [NSMapTable weakToStrongObjectsMapTable];
        app.delegate = host;

        NSMenu *bar = [[NSMenu alloc] init];
        NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"Knooo"];
        [bar addItemWithTitle:@"Knooo" action:nil keyEquivalent:@""].submenu = appMenu;
        [appMenu addItemWithTitle:@"退出 Knooo" action:@selector(terminate:) keyEquivalent:@"q"];
        NSMenu *edit = [[NSMenu alloc] initWithTitle:@"编辑"];
        [bar addItemWithTitle:@"编辑" action:nil keyEquivalent:@""].submenu = edit;
        [edit addItemWithTitle:@"撤销" action:@selector(undo:) keyEquivalent:@"z"];
        [edit addItemWithTitle:@"剪切" action:@selector(cut:) keyEquivalent:@"x"];
        [edit addItemWithTitle:@"复制" action:@selector(copy:) keyEquivalent:@"c"];
        [edit addItemWithTitle:@"粘贴" action:@selector(paste:) keyEquivalent:@"v"];
        [edit addItemWithTitle:@"全选" action:@selector(selectAll:) keyEquivalent:@"a"];
        app.mainMenu = bar;

        NSRect screen = NSScreen.mainScreen.visibleFrame;
        host.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, MIN(1240, screen.size.width),
            MIN(800, screen.size.height - 30)) styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
            NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO];
        host.window.releasedWhenClosed = NO;
        host.window.delegate = host;
        host.window.title = @"Knooo";
        WKWebView *view = [[WKWebView alloc] initWithFrame:host.window.contentView.bounds
            configuration:[[WKWebViewConfiguration alloc] init]];
        view.navigationDelegate = host;
        view.UIDelegate = host;
        host.window.contentView = view;
        [host.window center];
        [host.window makeKeyAndOrderFront:nil];
        [app activateIgnoringOtherApps:YES];
        [view loadRequest:[NSURLRequest requestWithURL:host.startURL]];
        [app run];
    }
}
