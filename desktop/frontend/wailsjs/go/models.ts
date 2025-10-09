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

}

