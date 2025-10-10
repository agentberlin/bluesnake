export namespace main {
	
	export class CrawledUrl {
	    ID: number;
	    CrawlID: number;
	    URL: string;
	    Status: number;
	    Title: string;
	    Indexable: string;
	    Error: string;
	    CreatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new CrawledUrl(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.CrawlID = source["CrawlID"];
	        this.URL = source["URL"];
	        this.Status = source["Status"];
	        this.Title = source["Title"];
	        this.Indexable = source["Indexable"];
	        this.Error = source["Error"];
	        this.CreatedAt = source["CreatedAt"];
	    }
	}
	export class Crawl {
	    ID: number;
	    ProjectID: number;
	    CrawlDateTime: number;
	    CrawlDuration: number;
	    PagesCrawled: number;
	    CrawledUrls: CrawledUrl[];
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Crawl(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.ProjectID = source["ProjectID"];
	        this.CrawlDateTime = source["CrawlDateTime"];
	        this.CrawlDuration = source["CrawlDuration"];
	        this.PagesCrawled = source["PagesCrawled"];
	        this.CrawledUrls = this.convertValues(source["CrawledUrls"], CrawledUrl);
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
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
	export class Project {
	    ID: number;
	    URL: string;
	    Domain: string;
	    FaviconPath: string;
	    Crawls: Crawl[];
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.URL = source["URL"];
	        this.Domain = source["Domain"];
	        this.FaviconPath = source["FaviconPath"];
	        this.Crawls = this.convertValues(source["Crawls"], Crawl);
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
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
	export class Config {
	    ID: number;
	    ProjectID: number;
	    Domain: string;
	    JSRenderingEnabled: boolean;
	    Parallelism: number;
	    Project?: Project;
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.ProjectID = source["ProjectID"];
	        this.Domain = source["Domain"];
	        this.JSRenderingEnabled = source["JSRenderingEnabled"];
	        this.Parallelism = source["Parallelism"];
	        this.Project = this.convertValues(source["Project"], Project);
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
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
	    indexable: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new CrawlResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.status = source["status"];
	        this.title = source["title"];
	        this.indexable = source["indexable"];
	        this.error = source["error"];
	    }
	}
	export class CrawlResultDetailed {
	    crawlInfo: CrawlInfo;
	    results: CrawlResult[];
	
	    static createFrom(source: any = {}) {
	        return new CrawlResultDetailed(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.crawlInfo = this.convertValues(source["crawlInfo"], CrawlInfo);
	        this.results = this.convertValues(source["results"], CrawlResult);
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

}

