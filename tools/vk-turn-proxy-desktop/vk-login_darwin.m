// SPDX-License-Identifier: MIT
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

@interface VKLoginWindow : NSObject <NSWindowDelegate, WKUIDelegate>
@property(nonatomic, retain) NSWindow *window;
@property(nonatomic, retain) WKWebView *view;
@property(nonatomic, copy) NSString *cookies;
@property(nonatomic) BOOL closed;
@property(nonatomic) BOOL pending;
- (void)close;
- (void)readCookies;
@end

@implementation VKLoginWindow
- (void)windowWillClose:(NSNotification *)notification {
    [self close];
}

- (void)close {
    if (self.closed) return;
    self.closed = YES;
    [self.view stopLoading];
    self.view.UIDelegate = nil;
    self.window.delegate = nil;
    [self.window close];
    self.view = nil;
    self.window = nil;
    self.cookies = nil;
}

- (WKWebView *)webView:(WKWebView *)webView
 createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
  forNavigationAction:(WKNavigationAction *)action
       windowFeatures:(WKWindowFeatures *)features {
    if (!action.targetFrame) [webView loadRequest:action.request];
    return nil;
}

- (void)readCookies {
    if (self.closed || self.pending) return;
    self.pending = YES;
    [self.view.configuration.websiteDataStore.httpCookieStore getAllCookies:^(NSArray<NSHTTPCookie *> *cookies) {
        self.pending = NO;
        if (self.closed) return;
        NSMutableString *text = [NSMutableString stringWithString:@"# Netscape HTTP Cookie File\n"];
        NSCharacterSet *invalid = [NSCharacterSet characterSetWithCharactersInString:@";\x7f"];
        for (NSHTTPCookie *cookie in cookies) {
            NSString *domain = cookie.domain.lowercaseString;
            if ([domain hasPrefix:@"."]) domain = [domain substringFromIndex:1];
            BOOL wanted = ([cookie.name isEqualToString:@"remixsid"] &&
                           ( [domain isEqualToString:@"vk.com"] || [domain isEqualToString:@"vk.ru"])) ||
                          ([cookie.name isEqualToString:@"p"] &&
                           ([domain isEqualToString:@"login.vk.com"] || [domain isEqualToString:@"login.vk.ru"]));
            if (!wanted || cookie.value.length == 0) continue;
            BOOL valid = [cookie.value rangeOfCharacterFromSet:invalid].location == NSNotFound;
            for (NSUInteger i = 0; valid && i < cookie.value.length; i++) {
                valid = [cookie.value characterAtIndex:i] > 32;
            }
            if (!valid) continue;
            long long expiry = cookie.expiresDate ? (long long)cookie.expiresDate.timeIntervalSince1970 : 0;
            [text appendFormat:@"#HttpOnly_%@\tTRUE\t/\tTRUE\t%lld\t%@\t%@\n",
                               domain, expiry, cookie.name, cookie.value];
        }
        self.cookies = text;
    }];
}

- (void)dealloc {
    [_window release];
    [_view release];
    [_cookies release];
    [super dealloc];
}
@end

// Wails invokes bound Go methods off the AppKit thread. Keep native state on its queue.
void *vkLoginOpen(const char *title, const char *hint) {
    __block VKLoginWindow *login;
    dispatch_sync(dispatch_get_main_queue(), ^{
        login = [[VKLoginWindow alloc] init];
        NSWindow *window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 650, 760)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable
            backing:NSBackingStoreBuffered defer:NO];
        window.releasedWhenClosed = NO;
        window.title = [NSString stringWithUTF8String:title];
        window.minSize = NSMakeSize(480, 480);
        window.delegate = login;
        login.window = window;
        [window release];
        NSTextField *label = [NSTextField wrappingLabelWithString:[NSString stringWithUTF8String:hint]];
        label.frame = NSMakeRect(16, 708, 618, 40);
        label.autoresizingMask = NSViewWidthSizable | NSViewMinYMargin;
        [window.contentView addSubview:label];
        WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
        config.websiteDataStore = [WKWebsiteDataStore nonPersistentDataStore];
        WKWebView *view = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 650, 696) configuration:config];
        [config release];
        view.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        view.UIDelegate = login;
        login.view = view;
        [window.contentView addSubview:view];
        [view release];
        [window center];
        [window makeKeyAndOrderFront:nil];
        [view loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:@"https://vk.ru/"]]];
    });
    return login;
}

char *vkLoginRead(void *handle, int *closed) {
    __block char *result;
    dispatch_sync(dispatch_get_main_queue(), ^{
        VKLoginWindow *login = (VKLoginWindow *)handle;
        *closed = login.closed;
        result = strdup((login.cookies ?: @"").UTF8String);
        [login readCookies];
    });
    return result;
}

void vkLoginClose(void *handle) {
    dispatch_sync(dispatch_get_main_queue(), ^{
        VKLoginWindow *login = (VKLoginWindow *)handle;
        [login close];
        [login release];
    });
}
