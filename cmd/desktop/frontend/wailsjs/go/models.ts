export namespace types {
	
	export class BotAccess {
	    allowed: boolean;
	    domain: string;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new BotAccess(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allowed = source["allowed"];
	        this.domain = source["domain"];
	        this.message = source["message"];
	    }
	}
	export class ContentVisibilityResult {
	    score: number;
	    statusCode: number;
	    isError: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ContentVisibilityResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.score = source["score"];
	        this.statusCode = source["statusCode"];
	        this.isError = source["isError"];
	    }
	}
	export class AICrawlerData {
	    contentVisibility?: ContentVisibilityResult;
	    robotsTxt?: Record<string, BotAccess>;
	    httpCheck?: Record<string, BotAccess>;
	    checkedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new AICrawlerData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.contentVisibility = this.convertValues(source["contentVisibility"], ContentVisibilityResult);
	        this.robotsTxt = this.convertValues(source["robotsTxt"], BotAccess, true);
	        this.httpCheck = this.convertValues(source["httpCheck"], BotAccess, true);
	        this.checkedAt = source["checkedAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AICrawlerResponse {
	    data?: AICrawlerData;
	    ssrScreenshot?: string;
	    jsScreenshot?: string;
	    noJSScreenshot?: string;
	
	    static createFrom(source: any = {}) {
	        return new AICrawlerResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.data = this.convertValues(source["data"], AICrawlerData);
	        this.ssrScreenshot = source["ssrScreenshot"];
	        this.jsScreenshot = source["jsScreenshot"];
	        this.noJSScreenshot = source["noJSScreenshot"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ActiveCrawlStats {
	    crawlId: number;
	    total: number;
	    crawled: number;
	    queued: number;
	    html: number;
	    javascript: number;
	    css: number;
	    images: number;
	    fonts: number;
	    unvisited: number;
	    others: number;
	
	    static createFrom(source: any = {}) {
	        return new ActiveCrawlStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.crawlId = source["crawlId"];
	        this.total = source["total"];
	        this.crawled = source["crawled"];
	        this.queued = source["queued"];
	        this.html = source["html"];
	        this.javascript = source["javascript"];
	        this.css = source["css"];
	        this.images = source["images"];
	        this.fonts = source["fonts"];
	        this.unvisited = source["unvisited"];
	        this.others = source["others"];
	    }
	}
	
	export class ConfigResponse {
	    domain: string;
	    jsRenderingEnabled: boolean;
	    initialWaitMs: number;
	    scrollWaitMs: number;
	    finalWaitMs: number;
	    parallelism: number;
	    userAgent: string;
	    includeSubdomains: boolean;
	    discoveryMechanisms: string[];
	    sitemapURLs: string[];
	    checkExternalResources: boolean;
	    singlePageMode: boolean;
	    robotsTxtMode: string;
	    followInternalNofollow: boolean;
	    followExternalNofollow: boolean;
	    respectMetaRobotsNoindex: boolean;
	    respectNoindex: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConfigResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domain = source["domain"];
	        this.jsRenderingEnabled = source["jsRenderingEnabled"];
	        this.initialWaitMs = source["initialWaitMs"];
	        this.scrollWaitMs = source["scrollWaitMs"];
	        this.finalWaitMs = source["finalWaitMs"];
	        this.parallelism = source["parallelism"];
	        this.userAgent = source["userAgent"];
	        this.includeSubdomains = source["includeSubdomains"];
	        this.discoveryMechanisms = source["discoveryMechanisms"];
	        this.sitemapURLs = source["sitemapURLs"];
	        this.checkExternalResources = source["checkExternalResources"];
	        this.singlePageMode = source["singlePageMode"];
	        this.robotsTxtMode = source["robotsTxtMode"];
	        this.followInternalNofollow = source["followInternalNofollow"];
	        this.followExternalNofollow = source["followExternalNofollow"];
	        this.respectMetaRobotsNoindex = source["respectMetaRobotsNoindex"];
	        this.respectNoindex = source["respectNoindex"];
	    }
	}
	
	export class CrawlInfo {
	    id: number;
	    projectId: number;
	    crawlDateTime: number;
	    crawlDuration: number;
	    pagesCrawled: number;
	
	    static createFrom(source: any = {}) {
	        return new CrawlInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.projectId = source["projectId"];
	        this.crawlDateTime = source["crawlDateTime"];
	        this.crawlDuration = source["crawlDuration"];
	        this.pagesCrawled = source["pagesCrawled"];
	    }
	}
	export class CrawlProgress {
	    projectId: number;
	    crawlId: number;
	    domain: string;
	    url: string;
	    pagesCrawled: number;
	    totalDiscovered: number;
	    discoveredUrls: string[];
	    isCrawling: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CrawlProgress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.crawlId = source["crawlId"];
	        this.domain = source["domain"];
	        this.url = source["url"];
	        this.pagesCrawled = source["pagesCrawled"];
	        this.totalDiscovered = source["totalDiscovered"];
	        this.discoveredUrls = source["discoveredUrls"];
	        this.isCrawling = source["isCrawling"];
	    }
	}
	export class CrawlResult {
	    url: string;
	    status: number;
	    title: string;
	    metaDescription?: string;
	    contentHash?: string;
	    indexable: string;
	    contentType?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new CrawlResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.status = source["status"];
	        this.title = source["title"];
	        this.metaDescription = source["metaDescription"];
	        this.contentHash = source["contentHash"];
	        this.indexable = source["indexable"];
	        this.contentType = source["contentType"];
	        this.error = source["error"];
	    }
	}
	export class CrawlResultPaginated {
	    results: CrawlResult[];
	    nextCursor: number;
	    hasMore: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CrawlResultPaginated(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.results = this.convertValues(source["results"], CrawlResult);
	        this.nextCursor = source["nextCursor"];
	        this.hasMore = source["hasMore"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DomainFrameworkResponse {
	    domain: string;
	    framework: string;
	    detectedAt: number;
	    manuallySet: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DomainFrameworkResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.domain = source["domain"];
	        this.framework = source["framework"];
	        this.detectedAt = source["detectedAt"];
	        this.manuallySet = source["manuallySet"];
	    }
	}
	export class FrameworkInfo {
	    id: string;
	    name: string;
	    category: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new FrameworkInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.description = source["description"];
	    }
	}
	export class LinkInfo {
	    url: string;
	    linkType: string;
	    anchorText: string;
	    context?: string;
	    isInternal: boolean;
	    status?: number;
	    position?: string;
	    domPath?: string;
	    urlAction: string;
	
	    static createFrom(source: any = {}) {
	        return new LinkInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.linkType = source["linkType"];
	        this.anchorText = source["anchorText"];
	        this.context = source["context"];
	        this.isInternal = source["isInternal"];
	        this.status = source["status"];
	        this.position = source["position"];
	        this.domPath = source["domPath"];
	        this.urlAction = source["urlAction"];
	    }
	}
	export class PageLinksResponse {
	    pageUrl: string;
	    inlinks: LinkInfo[];
	    outlinks: LinkInfo[];
	
	    static createFrom(source: any = {}) {
	        return new PageLinksResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pageUrl = source["pageUrl"];
	        this.inlinks = this.convertValues(source["inlinks"], LinkInfo);
	        this.outlinks = this.convertValues(source["outlinks"], LinkInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProjectInfo {
	    id: number;
	    url: string;
	    domain: string;
	    faviconPath: string;
	    crawlDateTime: number;
	    crawlDuration: number;
	    pagesCrawled: number;
	    latestCrawlId: number;
	
	    static createFrom(source: any = {}) {
	        return new ProjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.url = source["url"];
	        this.domain = source["domain"];
	        this.faviconPath = source["faviconPath"];
	        this.crawlDateTime = source["crawlDateTime"];
	        this.crawlDuration = source["crawlDuration"];
	        this.pagesCrawled = source["pagesCrawled"];
	        this.latestCrawlId = source["latestCrawlId"];
	    }
	}
	export class ServerInfo {
	    publicURL: string;
	    port: number;
	
	    static createFrom(source: any = {}) {
	        return new ServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.publicURL = source["publicURL"];
	        this.port = source["port"];
	    }
	}
	export class ServerStatus {
	    isRunning: boolean;
	    publicURL: string;
	    port: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isRunning = source["isRunning"];
	        this.publicURL = source["publicURL"];
	        this.port = source["port"];
	        this.error = source["error"];
	    }
	}
	export class UpdateInfo {
	    currentVersion: string;
	    latestVersion: string;
	    updateAvailable: boolean;
	    downloadUrl: string;
	    shouldWarn: boolean;
	    shouldBlock: boolean;
	    displayReason?: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.updateAvailable = source["updateAvailable"];
	        this.downloadUrl = source["downloadUrl"];
	        this.shouldWarn = source["shouldWarn"];
	        this.shouldBlock = source["shouldBlock"];
	        this.displayReason = source["displayReason"];
	    }
	}

}

