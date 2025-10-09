export namespace main {
	
	export class Config {
	    ID: number;
	    Domain: string;
	    JSRenderingEnabled: boolean;
	    Parallelism: number;
	    CreatedAt: number;
	    UpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Domain = source["Domain"];
	        this.JSRenderingEnabled = source["JSRenderingEnabled"];
	        this.Parallelism = source["Parallelism"];
	        this.CreatedAt = source["CreatedAt"];
	        this.UpdatedAt = source["UpdatedAt"];
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
	        this.crawlDateTime = source["crawlDateTime"];
	        this.crawlDuration = source["crawlDuration"];
	        this.pagesCrawled = source["pagesCrawled"];
	        this.latestCrawlId = source["latestCrawlId"];
	    }
	}

}

